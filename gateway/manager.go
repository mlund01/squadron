package gateway

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/humaninput"
	"squadron/store"
)

// Config is the host-side description of a gateway to launch. Built
// from the HCL `gateway "name" { ... }` block.
type Config struct {
	// Name from the HCL label.
	Name string
	// Source is the GitHub source ("github.com/owner/repo"). Empty
	// when the operator has placed the binary at the cached path
	// manually (mirrors the local-version override path plugins use).
	Source string
	// Version is the release tag to install.
	Version string
	// Settings is the HCL `settings = { ... }` map, passed through to
	// the gateway's Configure method.
	Settings map[string]string
}

// gatewayClient is the subset of the host-side gRPC client interface
// the manager relies on. Defined as an interface so tests can supply a
// fake without spawning a real subprocess.
type gatewayClient interface {
	Configure(ctx context.Context, settings map[string]string) error
	OnHumanInputRequested(ctx context.Context, rec gwsdk.HumanInputRecord) error
	OnHumanInputResolved(ctx context.Context, rec gwsdk.HumanInputRecord) error
	Shutdown(ctx context.Context) error
}

// subprocess represents the lifecycle of a launched gateway binary.
// `Exited` reports whether the underlying OS process is still running;
// `Kill` terminates it. For real gateways this is a thin wrapper around
// hashicorp/go-plugin's Client. For tests it's a fake.
type subprocess interface {
	Exited() bool
	Kill()
}

// launcher knows how to bring a gateway subprocess up. It returns the
// gRPC client used to talk to the gateway plus the OS-level handle the
// watchdog inspects for crashes.
//
// Defaulted to defaultLauncher in NewManager; tests inject their own to
// drive watchdog behavior without spawning binaries.
type launcher func(ctx context.Context, cfg Config, host *gwsdk.HostPlugin) (gatewayClient, subprocess, error)

// Manager owns the lifecycle of the (single) gateway subprocess. It
// subscribes to humaninput events, dispatches them to the gateway, and
// exposes a SquadronAPI implementation the gateway can call back into.
//
// A watchdog goroutine watches the subprocess for crashes and re-runs
// the launch+configure flow with the same Config when it dies. Stop()
// cancels the watchdog cleanly so a deliberate teardown isn't
// misinterpreted as a crash.
//
// Squadron supports at most one gateway per instance for now — the HCL
// parser enforces the singleton — so the manager intentionally holds a
// single client rather than a registry.
type Manager struct {
	stores   *store.Bundle
	notifier *humaninput.Notifier
	listener humaninput.Listener
	launch   launcher

	// watchdogInterval is how often the watchdog polls subprocess.Exited().
	// Exposed for tests so they don't pay multi-second sleeps.
	watchdogInterval time.Duration
	// initialBackoff is the wait before the first restart attempt after a
	// crash. Doubles up to maxBackoff on consecutive failures.
	initialBackoff time.Duration
	maxBackoff     time.Duration

	mu     sync.Mutex
	cfg    *Config
	client subprocess
	gw     gatewayClient

	cancelEvents context.CancelFunc
	eventDone    chan struct{}

	cancelWatchdog context.CancelFunc
	watchdogDone   chan struct{}
}

// NewManager constructs a manager. stores and notifier are required;
// listener is the wsbridge-side per-tool-call listener registry, used
// to wake the agent's blocking AskHuman call when a gateway resolves.
func NewManager(stores *store.Bundle, notifier *humaninput.Notifier, listener humaninput.Listener) *Manager {
	return &Manager{
		stores:           stores,
		notifier:         notifier,
		listener:         listener,
		launch:           defaultLauncher,
		watchdogInterval: 2 * time.Second,
		initialBackoff:   time.Second,
		maxBackoff:       30 * time.Second,
	}
}

// Start brings the gateway subprocess up: install (if needed), launch,
// configure, begin dispatching events, and arm the restart watchdog.
// Idempotent — a second Start with the same config is a no-op; with a
// different config it stops the running gateway first.
func (m *Manager) Start(ctx context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg != nil {
		if sameConfig(m.cfg, &cfg) {
			return nil
		}
		// Different gateway requested — tear the old one down first.
		m.stopLocked()
	}

	if err := m.launchLocked(ctx, cfg); err != nil {
		return err
	}

	// Watchdog polls subprocess.Exited and restarts on crash. Uses a
	// fresh context independent of ctx so a Configure-time ctx
	// cancellation doesn't kill the long-running watchdog. Stop()
	// cancels it explicitly.
	wctx, wcancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.cancelWatchdog = wcancel
	m.watchdogDone = done
	go m.watchdog(wctx, done)

	log.Printf("gateway %q started (version %s)", cfg.Name, cfg.Version)
	return nil
}

// launchLocked installs (if needed), launches the subprocess, calls
// Configure, and starts the dispatch loop. Caller holds m.mu.
//
// Used both from Start and from the watchdog's restart path so a crash
// recovery follows the exact same sequence as initial startup.
func (m *Manager) launchLocked(ctx context.Context, cfg Config) error {
	host := &gwsdk.HostPlugin{API: &squadronAPI{
		stores:   m.stores,
		notifier: m.notifier,
		listener: m.listener,
	}}

	gw, proc, err := m.launch(ctx, cfg, host)
	if err != nil {
		return err
	}

	if err := gw.Configure(ctx, cfg.Settings); err != nil {
		proc.Kill()
		return fmt.Errorf("gateway %q: configure: %w", cfg.Name, err)
	}

	m.client = proc
	m.gw = gw
	m.cfg = &cfg

	// Subscribe synchronously so the caller is guaranteed that an
	// event published immediately after Start returns will reach the
	// dispatcher. (If we subscribed inside the goroutine, the
	// goroutine schedule could race with the publisher and drop early
	// events.)
	events, cancelSub := m.notifier.Subscribe()
	dispatchCtx, cancel := context.WithCancel(context.Background())
	m.cancelEvents = cancel
	m.eventDone = make(chan struct{})
	go m.dispatchLoop(dispatchCtx, events, cancelSub)

	return nil
}

// Stop tears the gateway down, including the watchdog. Safe to call
// multiple times.
func (m *Manager) Stop() {
	// Cancel the watchdog without holding m.mu so a watchdog-in-flight
	// restart can finish its mu-holding section and observe the
	// cancellation on the next loop iteration.
	m.mu.Lock()
	cancelWD := m.cancelWatchdog
	doneWD := m.watchdogDone
	m.cancelWatchdog = nil
	m.watchdogDone = nil
	m.mu.Unlock()
	if cancelWD != nil {
		cancelWD()
		<-doneWD
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// stopLocked tears down the dispatch loop + subprocess but not the
// watchdog. Caller holds m.mu.
//
// Used by Start (replacing one gateway with another) and Stop. The
// watchdog's restart path uses the lower-level tearDownClientLocked
// instead because it wants to keep the watchdog alive.
func (m *Manager) stopLocked() {
	m.tearDownClientLocked()
	if m.cfg != nil {
		log.Printf("gateway %q stopped", m.cfg.Name)
	}
	m.cfg = nil
}

// tearDownClientLocked stops the dispatch loop and kills the
// subprocess but leaves m.cfg intact. Caller holds m.mu. Used by both
// stopLocked and the watchdog's restart path.
func (m *Manager) tearDownClientLocked() {
	if m.cancelEvents != nil {
		m.cancelEvents()
		<-m.eventDone
		m.cancelEvents = nil
		m.eventDone = nil
	}
	if m.gw != nil {
		// Best-effort graceful shutdown before killing the subprocess.
		_ = m.gw.Shutdown(context.Background())
	}
	if m.client != nil && !m.client.Exited() {
		m.client.Kill()
	}
	m.client = nil
	m.gw = nil
}

// watchdog polls m.client.Exited at watchdogInterval. When the
// subprocess has died (and Stop hasn't been called), it tears the
// dead client down and re-runs launchLocked with the original Config.
// Restart failures back off exponentially capped at maxBackoff.
//
// The done channel is passed in (not read from the struct) so Stop
// can clear m.watchdogDone after capturing it, without racing the
// watchdog's deferred close.
func (m *Manager) watchdog(ctx context.Context, done chan struct{}) {
	defer close(done)
	backoff := m.initialBackoff
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.watchdogInterval):
		}

		m.mu.Lock()
		if m.cfg == nil {
			m.mu.Unlock()
			return
		}
		// Two situations require a launch attempt:
		//   1. m.client != nil and the subprocess has exited (a crash
		//      we're discovering for the first time).
		//   2. m.client == nil — a previous restart attempt failed
		//      Configure, so we left the slot empty for the watchdog
		//      to retry on the next tick.
		needsLaunch := m.client == nil || m.client.Exited()
		if !needsLaunch {
			m.mu.Unlock()
			backoff = m.initialBackoff
			continue
		}
		cfg := *m.cfg
		if m.client != nil {
			log.Printf("gateway %q: subprocess exited unexpectedly — restarting", cfg.Name)
			m.tearDownClientLocked()
		}
		err := m.launchLocked(ctx, cfg)
		m.mu.Unlock()

		if err == nil {
			log.Printf("gateway %q: restart succeeded", cfg.Name)
			backoff = m.initialBackoff
			continue
		}

		log.Printf("gateway %q: restart failed: %v (next attempt in %s)", cfg.Name, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > m.maxBackoff {
			backoff = m.maxBackoff
		}
	}
}

// dispatchLoop forwards notifier events to the gateway. The
// subscription is created synchronously by launchLocked and handed in,
// so Start returning implies a live subscription — events published
// immediately after Start cannot race ahead of the dispatcher.
func (m *Manager) dispatchLoop(ctx context.Context, events <-chan humaninput.Event, cancelSub func()) {
	defer close(m.eventDone)
	defer cancelSub()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			m.dispatch(ctx, ev)
		}
	}
}

func (m *Manager) dispatch(ctx context.Context, ev humaninput.Event) {
	m.mu.Lock()
	gw := m.gw
	name := ""
	if m.cfg != nil {
		name = m.cfg.Name
	}
	m.mu.Unlock()
	if gw == nil {
		return
	}

	rec := storeRecordToSDK(ev.Record)
	switch ev.Kind {
	case humaninput.EventKindCreated:
		if err := gw.OnHumanInputRequested(ctx, rec); err != nil {
			log.Printf("gateway %q OnHumanInputRequested: %v", name, err)
		}
	case humaninput.EventKindResolved:
		if err := gw.OnHumanInputResolved(ctx, rec); err != nil {
			log.Printf("gateway %q OnHumanInputResolved: %v", name, err)
		}
	}
}

func sameConfig(a, b *Config) bool {
	if a.Name != b.Name || a.Source != b.Source || a.Version != b.Version {
		return false
	}
	if len(a.Settings) != len(b.Settings) {
		return false
	}
	for k, v := range a.Settings {
		if b.Settings[k] != v {
			return false
		}
	}
	return true
}

// defaultLauncher is the production launcher. It locates / installs the
// gateway binary, hands hashicorp/go-plugin a CommandContext for it,
// dispenses the registered plugin, and returns the gRPC client adapter
// plus a subprocess wrapper for the watchdog.
func defaultLauncher(ctx context.Context, cfg Config, host *gwsdk.HostPlugin) (gatewayClient, subprocess, error) {
	bin, err := ensureInstalled(cfg.Name, cfg.Version, cfg.Source)
	if err != nil {
		return nil, nil, err
	}

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: gwsdk.HostHandshake(),
		Plugins:         host.PluginMap(),
		Cmd:             exec.CommandContext(ctx, bin),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
		},
		Logger: hclog.New(&hclog.LoggerOptions{
			Name:   "gateway:" + cfg.Name,
			Level:  hclog.Info,
			Output: log.Default().Writer(),
		}),
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("gateway %q: dial subprocess: %w", cfg.Name, err)
	}

	raw, err := rpcClient.Dispense(gwsdk.PluginName)
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("gateway %q: dispense: %w", cfg.Name, err)
	}

	gw, ok := raw.(*gwsdk.GRPCGatewayClient)
	if !ok {
		client.Kill()
		return nil, nil, fmt.Errorf("gateway %q: unexpected client type %T", cfg.Name, raw)
	}

	return gw, pluginSubprocess{client}, nil
}

// pluginSubprocess adapts a hashicorp/go-plugin Client to the
// subprocess interface so the manager can poll for crashes without
// caring about the concrete plugin type.
type pluginSubprocess struct{ *plugin.Client }
