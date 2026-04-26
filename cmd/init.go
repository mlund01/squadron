package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"squadron/config"
	"squadron/config/vault"
	"squadron/internal/paths"

	"github.com/spf13/cobra"
)

var (
	initPassphraseFile string
	initVaultProvider  string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the encrypted vault for secret storage",
	Long: `Create the .squadron directory in the current working directory
and set up an encrypted vault for secret storage.

A cryptographically random passphrase is generated and stored via the
configured vault provider:

  file     (default) — passphrase is written to .squadron/vault.key
                       (0600 perms). No OS keychain prompts.
  keychain          — passphrase is stored in the OS keychain (macOS
                       Keychain, Linux Secret Service, Windows Cred
                       Manager). More secure at rest but triggers a
                       password / passkey prompt on first access per
                       process.

Use --passphrase-file to provide your own passphrase instead of
auto-generating one.

For guided setup that also picks a provider, stores an API key, and
generates a starter mission, use 'squadron quickstart' instead.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := RunInit(initPassphraseFile, initVaultProvider); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initPassphraseFile, "passphrase-file", "", "Path to file containing vault passphrase")
	initCmd.Flags().StringVar(&initVaultProvider, "vault-provider", vault.ProviderFile,
		fmt.Sprintf("Vault provider: %q or %q", vault.ProviderFile, vault.ProviderKeychain))
}

// RunInit performs the initialization logic. Called by quickstart and the
// --init flag on engage/chat/mission.
func RunInit(passphraseFile, providerName string) error {
	if err := paths.EnsureHome(); err != nil {
		return fmt.Errorf("creating squadron home: %w", err)
	}

	vaultPath, err := config.GetVaultFilePath()
	if err != nil {
		return err
	}

	v := vault.Open(vaultPath)
	if v.Exists() {
		fmt.Println("Squadron is already initialized.")
		return nil
	}

	provider, err := vault.ProviderByName(providerName)
	if err != nil {
		return err
	}

	var passphrase []byte
	switch {
	case passphraseFile != "":
		passphrase, err = vault.ReadPassphraseFile(passphraseFile)
		if err != nil {
			return fmt.Errorf("reading passphrase file: %w", err)
		}
	default:
		if data, readErr := vault.ReadPassphraseFile(vault.DockerSecretPath); readErr == nil {
			passphrase = data
		} else {
			passphrase, err = vault.GeneratePassphrase()
			if err != nil {
				return fmt.Errorf("generating passphrase: %w", err)
			}
		}
	}
	defer vault.ZeroBytes(passphrase)

	// Best effort: a keychain backend may fail in Docker / CI.
	if storeErr := provider.Store(passphrase); storeErr != nil {
		if passphraseFile == "" {
			if _, dockerErr := os.Stat(vault.DockerSecretPath); dockerErr != nil {
				fmt.Fprintf(os.Stderr, "Note: %s provider unavailable (%v). Using default passphrase.\n", provider.Name(), storeErr)
				passphrase = []byte(vault.FallbackPassphrase)
			}
		}
	}

	// Migrate existing vars.txt if present
	vars := make(map[string]string)
	varsPath, err := config.GetVarsFilePath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(varsPath); statErr == nil {
		vars, err = config.LoadPlaintextVars(varsPath)
		if err != nil {
			return fmt.Errorf("reading existing vars.txt: %w", err)
		}
		if len(vars) > 0 {
			fmt.Printf("Migrating %d variables from vars.txt to encrypted vault...\n", len(vars))
		}
	}

	// Create vault
	if err := v.Save(passphrase, vars); err != nil {
		return fmt.Errorf("creating vault: %w", err)
	}

	// Delete old vars.txt
	if _, statErr := os.Stat(varsPath); statErr == nil {
		if err := os.Remove(varsPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not delete vars.txt: %v\n", err)
		}
	}

	// Cache for current process
	vault.CachePassphrase(passphrase)

	if err := ensureSquadronGitignored(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update .gitignore: %v\n", err)
	}

	fmt.Printf("Squadron initialized with %q vault provider. Secrets are now encrypted at rest.\n", provider.Name())
	return nil
}

// ensureSquadronGitignored adds `.squadron/` to the .gitignore at the current
// working directory, creating the file if necessary. Runs unconditionally —
// even if the project isn't a git repo yet, so the entry is in place once
// it becomes one.
func ensureSquadronGitignored() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	gitignorePath := filepath.Join(cwd, ".gitignore")
	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if gitignoreContains(existing, ".squadron") {
		return nil
	}

	var buf strings.Builder
	if len(existing) > 0 {
		buf.Write(existing)
		if existing[len(existing)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	buf.WriteString(".squadron/\n")

	if err := os.WriteFile(gitignorePath, []byte(buf.String()), 0644); err != nil {
		return err
	}
	if len(existing) == 0 {
		fmt.Println("Created .gitignore with .squadron/ entry.")
	} else {
		fmt.Println("Added .squadron/ to .gitignore.")
	}
	return nil
}

// gitignoreContains checks whether any non-comment line in data matches the
// .squadron entry in any common form (.squadron, .squadron/, /.squadron, etc.).
func gitignoreContains(data []byte, entry string) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == entry {
			return true
		}
	}
	return false
}

// EnsureInitialized checks that squadron has been initialized.
// If autoInit is true, runs init automatically.
func EnsureInitialized(autoInit bool) error {
	if config.IsVaultInitialized() {
		return nil
	}

	if !autoInit {
		return fmt.Errorf("squadron not initialized. Run 'squadron init' (or 'squadron quickstart' for guided setup), or pass --init")
	}

	fmt.Println("Auto-initializing Squadron...")
	return RunInit("", vault.ProviderFile)
}

// promptYesNo asks a yes/no question on stdin; default is no.
func promptYesNo(question string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s [y/N]: ", question)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}
