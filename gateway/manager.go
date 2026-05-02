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

// Config is the host-side description of a gateway to launch.
// Built from the HCL `gateway "name" { ... }` block.
type Config struct {
	Name string
	// Source is the GitHub source ("github.com/owner/repo"). Empty
	// when the operator has placed the binary at the cached path
	// manually (mirrors the local-version override path plugins use).
	Source   string
	Version  string
	Settings map[string]string
}

// gatewayClient is the subset of the host-side gRPC client interface
// the manager uses; injectable so tests can swap in a fake.
type gatewayClient interface {
	Configure(ctx context.Context, settings map[string]string) error
	OnHumanInputRequested(ctx context.Context, rec gwsdk.HumanInputRecord) error
	OnHumanInputResolved(ctx context.Context, rec gwsdk.HumanInputRecord) error
	Shutdown(ctx context.Context) error
}

// subprocess wraps the OS-level handle the watchdog inspects.
type subprocess interface {
	Exited() bool
	Kill()
}

// launcher brings up a subprocess and returns its gRPC client +
// process handle. defaultLauncher is the production impl; tests
// inject their own to drive watchdog behavior without spawning bins.
type launcher func(ctx context.Context, cfg Config, host *gwsdk.HostPlugin) (gatewayClient, subprocess, error)

// Manager owns a single gateway subprocess: launch + configure + event
// dispatch + restart-on-crash watchdog. The HCL parser enforces the
// singleton (one gateway block per config) so we hold a single client
// rather than a registry.
type Manager struct {
	stores   *store.Bundle
	notifier *humaninput.Notifier
	listener humaninput.Listener
	launch   launcher

	// Tunables — exposed mainly so tests can shrink them.
	watchdogInterval time.Duration
	initialBackoff   time.Duration
	maxBackoff       time.Duration

	mu     sync.Mutex
	cfg    *Config
	client subprocess
	gw     gatewayClient

	cancelEvents context.CancelFunc
	eventDone    chan struct{}

	cancelWatchdog context.CancelFunc
	watchdogDone   chan struct{}
}

// NewManager — listener is the wsbridge per-tool-call registry, used
// to wake a blocking AskHuman call when a gateway resolves.
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

// Start launches the gateway and arms the watchdog. Idempotent on
// identical Config; replaces the running gateway when Config differs.
func (m *Manager) Start(ctx context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg != nil {
		if sameConfig(m.cfg, &cfg) {
			return nil
		}
		m.stopLocked()
	}

	if err := m.launchLocked(ctx, cfg); err != nil {
		return err
	}

	// Watchdog uses an independent context so a Configure-time ctx
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

// launchLocked runs the full launch + configure + dispatcher startup
// sequence. Used by both Start and the watchdog so crash-recovery
// follows exactly the same sequence as initial startup. Caller holds
// m.mu.
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

	// Subscribe synchronously so an event published immediately after
	// Start returns can't race ahead of the dispatcher goroutine.
	events, cancelSub := m.notifier.Subscribe()
	dispatchCtx, cancel := context.WithCancel(context.Background())
	m.cancelEvents = cancel
	m.eventDone = make(chan struct{})
	go m.dispatchLoop(dispatchCtx, events, cancelSub)

	return nil
}

// Stop is safe to call multiple times.
func (m *Manager) Stop() {
	// Cancel the watchdog without holding m.mu so a watchdog mid-restart
	// can finish its mu-holding section and observe the cancellation.
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

// stopLocked tears down dispatch + subprocess and clears m.cfg, but
// leaves the watchdog alone. Caller holds m.mu.
func (m *Manager) stopLocked() {
	m.tearDownClientLocked()
	if m.cfg != nil {
		log.Printf("gateway %q stopped", m.cfg.Name)
	}
	m.cfg = nil
}

// tearDownClientLocked stops the dispatcher and kills the subprocess
// without touching m.cfg or the watchdog. The watchdog's restart path
// uses this so it can re-launch under the same Config. Caller holds m.mu.
func (m *Manager) tearDownClientLocked() {
	if m.cancelEvents != nil {
		m.cancelEvents()
		<-m.eventDone
		m.cancelEvents = nil
		m.eventDone = nil
	}
	if m.gw != nil {
		_ = m.gw.Shutdown(context.Background())
	}
	if m.client != nil && !m.client.Exited() {
		m.client.Kill()
	}
	m.client = nil
	m.gw = nil
}

// watchdog re-launches when the subprocess crashes. Restart failures
// back off exponentially up to maxBackoff. The done channel is passed
// in (not read from the struct) so Stop can capture and clear
// m.watchdogDone without racing the deferred close here.
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
		// m.client == nil happens when a previous restart failed
		// Configure — we left the slot empty for the next tick to retry.
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

func defaultLauncher(ctx context.Context, cfg Config, host *gwsdk.HostPlugin) (gatewayClient, subprocess, error) {
	bin, err := EnsureInstalled(cfg.Name, cfg.Version, cfg.Source)
	if err != nil {
		return nil, nil, err
	}

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  gwsdk.HostHandshake(),
		Plugins:          host.PluginMap(),
		Cmd:              exec.CommandContext(ctx, bin),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
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

// pluginSubprocess satisfies subprocess for the real go-plugin Client.
type pluginSubprocess struct{ *plugin.Client }
