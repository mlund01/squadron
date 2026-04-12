package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"squadron/config"
	squadronmcp "squadron/mcp"
	"squadron/mcp/oauth"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage MCP server connections",
	Long: `Commands for managing OAuth-authenticated MCP server connections.

These commands read the mcp "name" { ... } blocks from your HCL config to
discover which servers exist, then interact with the vault to persist tokens
and client credentials.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if !config.IsVaultInitialized() {
			fmt.Fprintln(os.Stderr, "Error: Squadron not initialized. Run 'squadron init' first.")
			os.Exit(1)
		}
	},
}

func loadMCPSpecs(configPath string) (map[string]config.MCPServer, error) {
	servers, err := config.LoadMCPSpecs(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	out := make(map[string]config.MCPServer, len(servers))
	for _, s := range servers {
		out[s.Name] = s
	}
	return out, nil
}

func requireHTTPServer(specs map[string]config.MCPServer, name string) (config.MCPServer, error) {
	spec, ok := specs[name]
	if !ok {
		return config.MCPServer{}, fmt.Errorf("mcp %q: not found in config", name)
	}
	if spec.URL == "" {
		return config.MCPServer{}, fmt.Errorf("mcp %q: OAuth only applies to HTTP (url) servers, this one is stdio (%s)",
			name, spec.Location())
	}
	if _, hasAuthHeader := spec.Headers["Authorization"]; hasAuthHeader {
		return config.MCPServer{}, fmt.Errorf("mcp %q: a static Authorization header is configured; remove it from the HCL block before using OAuth", name)
	}
	return spec, nil
}

var mcpLoginCmd = &cobra.Command{
	Use:   "login <name>",
	Short: "Authorize an MCP server via OAuth",
	Long: `Runs the OAuth 2.1 flow for the named mcp "name" { ... } server:

  1. Discovers the authorization server from the MCP URL's .well-known metadata
  2. Registers squadron as an OAuth client (DCR) if no cached credentials exist
  3. Starts a loopback HTTP server on an ephemeral port
  4. Opens your browser to the authorization URL
  5. Waits for the redirect, exchanges the code, and stores the token in the vault

A missing or expired token on a server that requires auth causes mission and
chat runs to fail with a pointer to this command.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		configPath, _ := cmd.Flags().GetString("config")

		specs, err := loadMCPSpecs(configPath)
		if err != nil {
			return err
		}
		spec, err := requireHTTPServer(specs, name)
		if err != nil {
			return err
		}

		// Probe the server first to determine whether login is actually needed.
		fmt.Printf("Checking mcp %q (%s)...\n", name, spec.URL)
		probeCtx, probeCancel := context.WithTimeout(cmd.Context(), probeTimeout)
		_, probeErr := squadronmcp.LoadWithContext(probeCtx, name, squadronmcp.Spec{
			Command: spec.Command,
			URL:     spec.URL,
			Headers: spec.Headers,
			Source:  spec.Source,
			Version: spec.Version,
			Entry:   spec.Entry,
			Args:    spec.Args,
			Env:     spec.Env,
		})
		probeCancel()

		if probeErr == nil {
			fmt.Printf("Server %q connected successfully without authentication. No login needed.\n", name)
			return nil
		}

		// Only proceed to the OAuth flow if the probe specifically identified
		// an auth requirement. Any other failure (timeout, connection refused,
		// server down) should be surfaced directly — running the OAuth flow
		// against a server that isn't responding or doesn't support OAuth
		// just produces a confusing secondary error.
		var authErr *squadronmcp.AuthRequiredError
		if !errors.As(probeErr, &authErr) {
			return fmt.Errorf("mcp %q: %w", name, probeErr)
		}

		// If the user provided a client_id (for servers that don't support DCR),
		// save it before running the flow so the orchestrator picks it up.
		clientID, _ := cmd.Flags().GetString("client-id")
		clientSecret, _ := cmd.Flags().GetString("client-secret")
		if clientID != "" {
			if err := oauth.SaveClientCredentials(name, oauth.ClientCredentials{
				ClientID:     clientID,
				ClientSecret: clientSecret,
			}); err != nil {
				return err
			}
		}

		fmt.Printf("Authorizing mcp %q (%s)...\n", name, spec.URL)

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		source := oauth.NewLoopbackCallbackSource()
		if err := oauth.RunLoginFlow(ctx, name, spec.URL, source); err != nil {
			return err
		}

		fmt.Printf("\nAuthorization successful. Token stored in vault.\n")
		return nil
	},
}

var mcpLogoutCmd = &cobra.Command{
	Use:   "logout <name>",
	Short: "Forget the stored OAuth token for an MCP server",
	Long: `Removes the vault entry for the given mcp server's access and refresh
tokens. The cached client_id from dynamic client registration is preserved so
that a subsequent 'squadron mcp login' skips the registration round-trip.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := oauth.DeleteToken(name); err != nil {
			return fmt.Errorf("deleting token for %q: %w", name, err)
		}
		fmt.Printf("Token for mcp %q removed.\n", name)
		return nil
	},
}

var mcpStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show connection status for every configured MCP server",
	Long: `Lists every mcp "name" { ... } block and its auth state.

For servers with a stored OAuth token, the state is derived from the vault
(no network call). For HTTP servers with no stored token, squadron probes
the server to determine whether it requires auth or is open/anonymous.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		specs, err := loadMCPSpecs(configPath)
		if err != nil {
			return err
		}

		if len(specs) == 0 {
			fmt.Println("No mcp servers configured.")
			return nil
		}

		snap, err := oauth.LoadVaultSnapshot()
		if err != nil {
			return fmt.Errorf("reading vault: %w", err)
		}

		names := make([]string, 0, len(specs))
		for n := range specs {
			names = append(names, n)
		}
		sort.Strings(names)

		// Identify HTTP servers with no stored token — we need to probe them.
		var toProbe []config.MCPServer
		for _, n := range names {
			spec := specs[n]
			if spec.URL != "" && !snap.HasToken(spec.Name) {
				if _, hasStatic := spec.Headers["Authorization"]; !hasStatic {
					toProbe = append(toProbe, spec)
				}
			}
		}

		// Probe in parallel with a spinner if there's anything to check.
		probeResults := make(map[string]probeResult)
		if len(toProbe) > 0 {
			sp := startSpinner(fmt.Sprintf("Probing %d MCP server(s)", len(toProbe)))
			probeResults = probeServers(toProbe, sp)
			sp.Stop()
		}

		fmt.Printf("%-20s %-50s %-15s %s\n", "NAME", "LOCATION", "AUTH", "EXPIRES")
		for _, n := range names {
			spec := specs[n]
			authState, expires := describeAuth(spec, snap, probeResults)
			fmt.Printf("%-20s %-50s %-15s %s\n",
				n,
				truncate(spec.Location(), 50),
				authState,
				expires,
			)
		}
		return nil
	},
}

// probeResult is the outcome of trying to connect to an MCP server that has
// no stored token.
type probeResult struct {
	connected bool  // true = loaded fine anonymously
	authReq   bool  // true = server returned 401 / needs OAuth
	err       error // non-nil for timeouts / transport failures
}

const probeTimeout = 10 * time.Second

// probeServers connects to each server in parallel and classifies the result.
func probeServers(servers []config.MCPServer, sp *spinner) map[string]probeResult {
	results := make(map[string]probeResult, len(servers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, srv := range servers {
		wg.Add(1)
		go func(s config.MCPServer) {
			defer wg.Done()
			sp.SetMessage(fmt.Sprintf("Probing %s", s.Name))

			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			defer cancel()

			_, loadErr := squadronmcp.LoadWithContext(ctx, s.Name, squadronmcp.Spec{
				Command: s.Command,
				URL:     s.URL,
				Headers: s.Headers,
				Source:  s.Source,
				Version: s.Version,
				Entry:   s.Entry,
				Args:    s.Args,
				Env:     s.Env,
			})

			var pr probeResult
			if loadErr == nil {
				pr.connected = true
			} else {
				var authErr *squadronmcp.AuthRequiredError
				if errors.As(loadErr, &authErr) {
					pr.authReq = true
				} else {
					pr.err = loadErr
				}
			}

			mu.Lock()
			results[s.Name] = pr
			mu.Unlock()
		}(srv)
	}

	wg.Wait()
	return results
}

func describeAuth(spec config.MCPServer, snap *oauth.VaultSnapshot, probes map[string]probeResult) (state, expires string) {
	if spec.URL == "" {
		return "n/a", "-"
	}
	if _, hasStatic := spec.Headers["Authorization"]; hasStatic {
		return "static header", "-"
	}

	// If we have a stored token, trust the vault — no probe needed.
	if snap.HasToken(spec.Name) {
		tok, err := snap.Token(spec.Name)
		if err != nil || tok == nil {
			return "unreadable", "-"
		}
		if tok.ExpiresAt.IsZero() {
			return "connected", "no expiry"
		}
		remaining := time.Until(tok.ExpiresAt)
		if remaining <= 0 {
			return "expired", "(refreshes on use)"
		}
		return "connected", humanDuration(remaining)
	}

	// No token — use the probe result.
	if pr, ok := probes[spec.Name]; ok {
		if pr.connected {
			return "open", "-"
		}
		if pr.authReq {
			return "needs login", "-"
		}
		if pr.err != nil {
			if errors.Is(pr.err, context.DeadlineExceeded) {
				return "timeout", "-"
			}
			return "error", "-"
		}
	}

	return "unknown", "-"
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("in %ds", int(d.Seconds()))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

var mcpRefreshCmd = &cobra.Command{
	Use:   "refresh <name>",
	Short: "Force-refresh an OAuth token",
	Long:  `Uses the stored refresh token to obtain a new access token from the authorization server.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		configPath, _ := cmd.Flags().GetString("config")

		specs, err := loadMCPSpecs(configPath)
		if err != nil {
			return err
		}
		spec, err := requireHTTPServer(specs, name)
		if err != nil {
			return err
		}

		if !oauth.HasToken(name) {
			return fmt.Errorf("mcp %q: no token stored — run 'squadron mcp login %s' first", name, name)
		}

		fmt.Printf("Refreshing token for mcp %q...\n", name)
		if err := oauth.ForceRefresh(cmd.Context(), name, spec.URL); err != nil {
			return err
		}
		fmt.Println("Token refreshed successfully.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpLoginCmd)
	mcpCmd.AddCommand(mcpLogoutCmd)
	mcpCmd.AddCommand(mcpStatusCmd)
	mcpCmd.AddCommand(mcpRefreshCmd)

	mcpLoginCmd.Flags().StringP("config", "c", ".", "Path to config directory or file")
	mcpLoginCmd.Flags().String("client-id", "", "OAuth client ID (for servers that don't support dynamic registration)")
	mcpLoginCmd.Flags().String("client-secret", "", "OAuth client secret (if required by the server)")
	mcpStatusCmd.Flags().StringP("config", "c", ".", "Path to config directory or file")
	mcpRefreshCmd.Flags().StringP("config", "c", ".", "Path to config directory or file")
}
