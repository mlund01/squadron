package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"squadron/agent"
	"squadron/config"
	"squadron/gateway"
	"squadron/humaninput"
	"squadron/internal/daemon"
	"squadron/internal/paths"
	squadronmcp "squadron/mcp"
	"squadron/mcphost"
	"squadron/mission"
	"squadron/notification"
	"squadron/store"
	"squadron/wsbridge"
)

var (
	engageConfigPath string
	legacyHeadless   bool
	legacyCCPort     int
	engageAutoInit   bool
	engageForeground bool
	engageReload     bool
)

var engageCmd = &cobra.Command{
	Use:   "engage",
	Short: "Start Squadron and connect to the command center",
	Long: `Start Squadron as a background service.

Squadron connects to a Command Center that has already been deployed for
this workspace. On its first interactive start, Squadron asks for the
Command Center URL and runner credential. It saves
that machine secret in .squadron/command-center.json beside this project.

Squadron runs in the background and installs as a system service (launchd
on macOS, systemd on Linux) so it starts automatically on boot.

Use --foreground to run in the terminal instead of the background.
Use 'squadron disengage' to stop Squadron and remove the system service.`,
	Run: runEngage,
}

func init() {
	rootCmd.AddCommand(engageCmd)

	engageCmd.Flags().StringVarP(&engageConfigPath, "config", "c", ".", "Path to config file or directory")
	// Keep accepting retired flags so upgrades do not break service scripts. A
	// worker always connects to its configured Command Center now.
	engageCmd.Flags().BoolVar(&legacyHeadless, "headless", false, "Deprecated: Squadron always connects to Command Center")
	engageCmd.Flags().IntVar(&legacyCCPort, "cc-port", 0, "Deprecated: Command Center is deployed independently")
	_ = engageCmd.Flags().MarkDeprecated("headless", "Squadron always connects to its configured Command Center")
	_ = engageCmd.Flags().MarkDeprecated("cc-port", "Command Center is deployed independently")
	engageCmd.Flags().BoolVar(&engageAutoInit, "init", false, "Auto-initialize Squadron if not already initialized")
	engageCmd.Flags().BoolVar(&engageForeground, "foreground", false, "Run in foreground (default: run as background service)")
	engageCmd.Flags().BoolVarP(&engageReload, "reload", "r", false, "Reload the config of an already-running squadron (no-op if not running)")
}

func runEngage(cmd *cobra.Command, args []string) {
	// In a container, the process IS the daemon — don't double-fork.
	if isContainer() {
		engageForeground = true
	}

	if err := applyHome(engageConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Only the user-invoked parent process should check IsRunning. The forked
	// child runs with --foreground and would otherwise see the PID file the
	// parent just wrote for it and bail out as "already running".
	if !engageForeground {
		running, pid := daemon.IsRunning(engageConfigPath)
		switch {
		case running && engageReload:
			reloadRunningSquadron(pid)
			return
		case running:
			fmt.Fprintf(os.Stderr, "Error: squadron is already running (PID %d).\n", pid)
			fmt.Fprintln(os.Stderr, "Use 'squadron engage -r' to reload the config, or 'squadron disengage' to stop it.")
			os.Exit(1)
		case engageReload:
			fmt.Println("Squadron is not running — ignoring -r and starting it now.")
		}
	}

	if warning, err := validateConfigDir(engageConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	} else if warning != "" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n\n", warning)
		if !promptYesNo("Continue anyway?") {
			return
		}
	}

	hasHCL := hasHCLFiles(engageConfigPath)
	if !hasHCL && term.IsTerminal(int(os.Stdin.Fd())) {
		// Fresh install on an interactive terminal — run the full wizard.
		dir, err := RunQuickstart(engageConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		engageConfigPath = dir
	}

	if err := EnsureInitialized(engageAutoInit); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	connectionProfile, err := ensureCommandCenterProfile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !engageForeground && term.IsTerminal(int(os.Stdout.Fd())) {
		absConfigPath, err := filepath.Abs(engageConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving config path: %v\n", err)
			os.Exit(1)
		}

		daemon.ClearReady(absConfigPath)
		sp := startSpinner("Starting Squadron")

		pid, err := daemon.Fork(absConfigPath, nil)
		if err != nil {
			sp.Stop()
			fmt.Fprintf(os.Stderr, "Error starting background process: %v\n", err)
			os.Exit(1)
		}

		ready := daemon.WaitReady(absConfigPath, 60*time.Second, 2*time.Second)
		sp.Stop()

		if !ready.OK {
			daemon.CleanupFailedFork(absConfigPath)
			fmt.Fprintf(os.Stderr, "Error: %s\n", ready.Error)
			os.Exit(1)
		}

		// Install as system service for boot persistence. Non-fatal: the process
		// is already running, we just won't auto-start next boot.
		if err := daemon.InstallService(absConfigPath); err != nil {
			log.Printf("Note: could not install system service: %v", err)
		}

		fmt.Printf("Squadron engaged (PID %d). Starts automatically on boot.\n", pid)
		fmt.Println("Use 'squadron disengage' to stop.")

		return
	}

	// --- Foreground mode ---

	daemon.ClearReady(engageConfigPath)

	// Forked daemons need to clean up the PID file when they exit so the next
	// `engage` doesn't see a stale PID. (`disengage` removes it on its own
	// path, but a crash or signal-driven shutdown would otherwise leave it.)
	if os.Getenv("SQUADRON_FORKED") == "1" {
		defer daemon.ClearPid(engageConfigPath)
	}

	// Start the MCP host BEFORE the full config load so consumer-side
	// `mcp` blocks can self-reference http://localhost:<port>/mcp without
	// deadlocking. We parse only the variable + mcp_host blocks here
	// (expressions resolve against the current in-memory vars context), bring
	// up the listening socket, and wire its tool handlers via closures
	// over variables assigned later in startup.
	var (
		sharedClient *wsbridge.Client
		sharedStores *store.Bundle
		mcpServer    *mcphost.Server
	)
	hostCfg, hostErr := config.LoadMCPHost(engageConfigPath)
	if hostErr != nil {
		log.Printf("Warning: MCP host not started: %v", hostErr)
	}
	if hostCfg != nil && hostCfg.Enabled {
		mcpDeps := mcphost.Deps{
			Config: func() *config.Config {
				if sharedClient == nil {
					return nil
				}
				return sharedClient.GetConfig()
			},
			Stores: func() *store.Bundle { return sharedStores },
			RunMission: func(name string, inputs map[string]string) (string, error) {
				if sharedClient == nil {
					return "", fmt.Errorf("squadron is still starting up")
				}
				return sharedClient.RunMissionDirect(name, inputs)
			},
			ReloadConfig: func() error {
				if sharedClient == nil {
					return fmt.Errorf("squadron is still starting up")
				}
				return sharedClient.ReloadConfig()
			},
			Version:    Version,
			ConfigPath: engageConfigPath,
		}
		mcpSrv := mcphost.NewServer(mcpDeps)
		var err error
		mcpServer, err = mcphost.StartStreamableHTTP(mcpSrv, hostCfg.Port, hostCfg.Secret)
		if err != nil {
			log.Printf("Warning: MCP host failed to start: %v", err)
		}
	}

	// Best-effort load — missing vars or partial validation errors don't stop
	// startup; Command Center can help an operator correct them in place.
	cfg, cfgErr := config.LoadPartial(engageConfigPath)
	if cfgErr != nil {
		log.Println("No valid configuration yet. Use the command center UI to create or edit HCL files.")
	}
	cfg.CommandCenter = &config.CommandCenterConfig{
		URL:          connectionProfile.URL,
		InstanceName: "workspace-runner",
	}

	// Pre-flight gateway install BEFORE signaling ready so the parent
	// CLI sees the failure (background-mode parent prints "Squadron
	// engaged" the instant ready fires; if we deferred the install until
	// after ready, a broken gateway would die invisibly post-fork).
	// The full gateway start happens later — EnsureInstalled is
	// idempotent so the cached binary is reused.
	if cfgErr == nil && cfg != nil && cfg.Gateway != nil {
		if _, err := gateway.EnsureInstalled(cfg.Gateway.Name, cfg.Gateway.Version, cfg.Gateway.Source); err != nil {
			wrapped := fmt.Errorf("gateway %q failed to install: %w", cfg.Gateway.Name, err)
			daemon.SignalFailed(engageConfigPath, wrapped)
			fmt.Fprintf(os.Stderr, "Error: %v\n", wrapped)
			os.Exit(1)
		}
	}

	daemon.SignalReady(engageConfigPath, 0)

	storageConfig := cfg.Storage
	if storageConfig == nil {
		storageConfig = config.DefaultStorageConfig(engageConfigPath)
	}
	stores, err := store.NewBundle(storageConfig)
	if err != nil {
		daemon.SignalFailed(engageConfigPath, fmt.Errorf("could not open storage: %w", err))
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer stores.Close()

	cfgErrMsg := ""
	if cfgErr != nil {
		cfgErrMsg = cfgErr.Error()
	}
	client := wsbridge.NewClient(cfg, cfgErr == nil, cfgErrMsg, engageConfigPath, stores, Version, connectionProfile.WorkerCredential)

	// In-process notifier — gateways subscribe to it for human-input
	// events. The wsbridge ask + resolve paths publish here in
	// addition to firing wire-protocol mission events for commander.
	notifier := humaninput.New()
	client.SetHumanInputNotifier(notifier)

	// Optional gateway subprocess. The HCL block is enforced singleton
	// at parse time so cfg.Gateway is at most one Gateway. A configured
	// gateway that fails to start is fatal — `squadron verify` already
	// pre-flights install, so by the time engage runs the binary should
	// be on disk; any failure here means the user has a broken setup
	// they need to see, not a silent half-running daemon.
	var gatewayMgr *gateway.Manager
	if cfgErr == nil && cfg != nil && cfg.Gateway != nil {
		gatewayMgr = gateway.NewManager(stores, notifier, client.HumanInputListener())
		if err := gatewayMgr.Start(context.Background(), gateway.Config{
			Name:     cfg.Gateway.Name,
			Source:   cfg.Gateway.Source,
			Version:  cfg.Gateway.Version,
			Settings: cfg.Gateway.Settings,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: gateway %q failed to start: %v\n", cfg.Gateway.Name, err)
			os.Exit(1)
		}
	}
	defer func() {
		if gatewayMgr != nil {
			gatewayMgr.Stop()
		}
	}()

	// Mission-lifecycle notification dispatcher. The gateway channel is
	// wired only when a gateway is configured; the command-center channel
	// no-ops when no command center is connected.
	var gatewaySink notification.Sink
	if gatewayMgr != nil {
		gatewaySink = gateway.NewNotifySink(gatewayMgr)
		// The Manager satisfies aitools.GatewayBridge directly; only wire it
		// when a gateway exists so the tool sees a nil bridge otherwise.
		client.SetGatewayBridge(gatewayMgr)
	}
	client.SetNotifier(notification.NewDispatcher(gatewaySink, wsbridge.NewNotifySink(client)))

	concurrency := wsbridge.NewMissionConcurrencyTracker(cfg)
	client.SetConcurrencyTracker(concurrency)
	// Command Center may supply model connections after startup. When the
	// initial config is incomplete, recover as soon as a reload succeeds.
	recoverOrphans := startOrphanRecoveryOnce(client.ResumeOrphanedMissions)

	client.OnConfigLoaded = func(newCfg *config.Config) {
		concurrency.UpdateConfig(newCfg)
		daemon.SignalReady(engageConfigPath, 0)
		recoverOrphans()
	}

	// Wire deferred deps now that client + stores exist. The MCP host is
	// already running (started above, before the full config load) so any
	// in-flight tool calls coming through it will start succeeding now.
	sharedClient = client
	sharedStores = stores

	// Install the shutdown handler BEFORE connectConfiguredWithRetry so that SIGTERM
	// during a long connection retry (e.g. background mode, command center
	// unreachable) closes the client cleanly instead of hard-killing the
	// process and orphaning child processes.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	shutdown := make(chan struct{})
	go func() {
		<-sigs
		close(shutdown)
		client.Close()
	}()

	reloads := make(chan os.Signal, 1)
	signal.Notify(reloads, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-shutdown:
				return
			case <-reloads:
				log.Println("SIGHUP received — reloading config")
				err := client.ReloadConfig()
				if err != nil {
					log.Printf("Config reload failed: %v", err)
					daemon.SignalFailed(engageConfigPath, err)
				} else {
					log.Println("Config reload succeeded")
				}
				client.NotifyConfigReloaded(err)
			}
		}
	}()

	// Periodic sweep of expired per-run ephemeral memory directories.
	// Runs hourly; walks the filesystem so the live config isn't needed.
	go runScratchpadCleanupLoop(shutdown)

	// Even without valid mission config, keep the worker connected so Command
	// Center can help an operator fix its configuration.
	if cfg.CommandCenter != nil {
		if err := connectConfiguredWithRetry(client); err != nil {
			log.Printf("Connection failed: %v (will retry when config changes)", err)
		} else {
			fmt.Printf("Squadron ready — connected to %s\n", cfg.CommandCenter.URL)
		}
	}

	// If SIGTERM fired during the connect retry, skip normal startup and
	// jump straight to cleanup.
	select {
	case <-shutdown:
		// fall through to shutdown below (the select at <-shutdown returns immediately)
	default:
		if cfgErr == nil {
			recoverOrphans()
		} else {
			// Watch for files to appear/change so we can retry the load.
			go watchForConfigChanges(client, engageConfigPath)
		}
	}

	// Websocket watchdog: for as long as squadron is running, keep the
	// connection to the command center alive. Any drop triggers an
	// indefinite reconnect — we only exit this loop on shutdown.
	go func() {
		for {
			err := client.Run()
			// Distinguish shutdown from a natural disconnect. Run sets
			// c.connected = false in both cases, so IsConnected() can't
			// tell them apart — check the shutdown channel instead.
			select {
			case <-shutdown:
				return
			default:
			}
			if err == nil {
				// Never connected (no command_center configured yet) —
				// nothing to reconnect to. Bail out; a config reload can
				// wire things up later.
				return
			}
			log.Printf("Connection lost: %v", err)
			cfg := client.GetConfig()
			if cfg == nil || cfg.CommandCenter == nil {
				// No URL to reconnect to yet — wait for config changes.
				go watchForConfigChanges(client, engageConfigPath)
				return
			}
			log.Println("Attempting to reconnect...")
			if err := connectConfiguredWithRetry(client); err != nil {
				// connectConfiguredWithRetry only returns an error on shutdown.
				return
			}
		}
	}()

	<-shutdown
	fmt.Println("Shutting down...")
	daemon.ClearReady(engageConfigPath)

	// client.Close() was already called by the signal handler; this is a
	// no-op second call, safe to leave for clarity.
	client.Close()

	squadronmcp.CloseAll() // close MCP clients before stopping the host
	if mcpServer != nil {
		mcpServer.Shutdown()
	}
}

func startOrphanRecoveryOnce(recoverMissions func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() { go recoverMissions() })
	}
}

// isContainer reports whether we're running inside a container. The official
// image sets SQUADRON_CONTAINER=1 in its ENV.
func isContainer() bool {
	return os.Getenv("SQUADRON_CONTAINER") == "1"
}

func reloadRunningSquadron(pid int) {
	absConfigPath, err := filepath.Abs(engageConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving config path: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Squadron is already running (PID %d). Reloading config from %s...\n", pid, absConfigPath)

	daemon.ClearReady(absConfigPath)

	if _, err := daemon.Reload(absConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error signaling squadron (PID %d): %v\n", pid, err)
		os.Exit(1)
	}

	sp := startSpinner("Validating and applying")
	ready := daemon.WaitReady(absConfigPath, 30*time.Second, 500*time.Millisecond)
	sp.Stop()

	if !ready.OK {
		fmt.Fprintf(os.Stderr, "Config reload failed: %s\n", ready.Error)
		fmt.Fprintf(os.Stderr, "Squadron is still running with the previous config (PID %d). Fix the error above and re-run 'squadron engage'.\n", pid)
		os.Exit(1)
	}

	fmt.Println("Config reloaded successfully.")
}

func hasHCLFiles(configPath string) bool {
	info, err := os.Stat(configPath)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return strings.HasSuffix(configPath, ".hcl")
	}
	entries, err := os.ReadDir(configPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
			return true
		}
	}
	return false
}

// validateConfigDir checks the config directory for common mistakes.
// Returns an error for hard failures, a warning string for soft issues.
func validateConfigDir(configPath string) (warning string, err error) {
	absPath, pathErr := filepath.Abs(configPath)
	if pathErr != nil {
		return "", pathErr
	}
	dir := absPath
	if info, statErr := os.Stat(absPath); statErr == nil && !info.IsDir() {
		dir = filepath.Dir(absPath)
	}

	// Error: nested .squadron directories — walk subdirectories (max 3 levels)
	// to detect projects inside projects.
	filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		// Skip the root .squadron itself
		if path == filepath.Join(dir, ".squadron") {
			return filepath.SkipDir
		}
		// Skip deep traversal
		rel, _ := filepath.Rel(dir, path)
		if strings.Count(rel, string(filepath.Separator)) > 3 {
			return filepath.SkipDir
		}
		if d.Name() == ".squadron" {
			err = fmt.Errorf("nested .squadron directory found at %s — squadron projects cannot be nested", path)
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Warning: directory is too high-level (home dir, filesystem root, etc.)
	home, _ := os.UserHomeDir()
	warnDirs := []string{"/", home}
	// Add common root dirs
	for _, d := range []string{"/tmp", "/var", "/etc", "/usr"} {
		warnDirs = append(warnDirs, d)
	}
	// Add home parent dirs (e.g. /Users, /Users/maxlund, /home, /home/maxlund)
	if home != "" {
		warnDirs = append(warnDirs, filepath.Dir(home))
	}

	for _, w := range warnDirs {
		if dir == w {
			warning = fmt.Sprintf("You are running in %s — this is a high-level directory.\n"+
				"  Consider creating a project directory first, e.g.:\n"+
				"    mkdir my-project && cd my-project", dir)
			break
		}
	}

	return warning, nil
}

// watchForConfigChanges polls the config path for changes,
// triggering a config reload when modifications are detected.
func watchForConfigChanges(client *wsbridge.Client, configPath string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastConfigMod := configDirModTime(configPath)

	for range ticker.C {
		if client.IsConnected() && client.HasConfig() {
			// Already connected with valid config — stop polling
			return
		}

		changed := false
		if t := configDirModTime(configPath); t.After(lastConfigMod) {
			lastConfigMod = t
			changed = true
		}

		if changed {
			log.Println("Detected changes, reloading config...")
			if err := client.ReloadConfig(); err != nil {
				log.Printf("Config still not ready: %v", err)
			}
		}
	}
}

// configDirModTime returns the latest modification time across all .hcl files in a config path.
func configDirModTime(configPath string) time.Time {
	var latest time.Time
	info, err := os.Stat(configPath)
	if err != nil {
		return latest
	}
	if !info.IsDir() {
		return info.ModTime()
	}
	entries, err := os.ReadDir(configPath)
	if err != nil {
		return latest
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
			if fi, err := e.Info(); err == nil && fi.ModTime().After(latest) {
				latest = fi.ModTime()
			}
		}
	}
	return latest
}

// connectConfiguredWithRetry uses the local Command Center profile already
// attached to the client config. Unlike ConnectTo, it preserves the worker
// credential and sends it during the WebSocket handshake.
func connectConfiguredWithRetry(client *wsbridge.Client) error {
	interval := 3 * time.Second

	for attempt := 1; ; attempt++ {
		err := client.Connect()
		if err == nil {
			return nil
		}
		log.Printf("Connection attempt %d failed: %v. Retrying in %v...", attempt, err, interval)
		select {
		case <-client.Done():
			return fmt.Errorf("shutting down: %w", err)
		case <-time.After(interval):
		}
	}
}

// moveFile copies src to dst then removes src. Works across filesystem boundaries.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Remove(src)
}

// runScratchpadCleanupLoop periodically sweeps expired per-run scratchpad and
// file-input directories. The sweeps walk the entire scratchpads / inputs
// trees, so they don't need to know which missions are configured. It runs
// once immediately, then hourly, and exits when shutdown is closed.
func runScratchpadCleanupLoop(shutdown <-chan struct{}) {
	sweep := func() {
		if _, err := mission.SweepExpiredScratchpads(); err != nil {
			log.Printf("scratchpad cleanup: %v", err)
		}
		if _, err := mission.SweepExpiredInputs(); err != nil {
			log.Printf("file-input cleanup: %v", err)
		}
		if home, err := paths.SquadronHome(); err != nil {
			log.Printf("managed-agent memory cleanup: %v", err)
		} else if _, err := agent.SweepExpiredManagedMemory(home); err != nil {
			log.Printf("managed-agent memory cleanup: %v", err)
		}
	}

	sweep()
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			sweep()
		}
	}
}
