package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/humaninput"
	"squadron/store"
)

func TestGatewayManager(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gateway Manager Suite")
}

// fakeSubprocess is a hand-rolled subprocess stand-in. The watchdog
// calls Exited() to decide whether to restart; tests flip the bool to
// simulate a crash. Kill is recorded so tests can assert teardown.
type fakeSubprocess struct {
	mu       sync.Mutex
	exited   bool
	killed   bool
	killCnt  int
}

func (f *fakeSubprocess) Exited() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exited
}

func (f *fakeSubprocess) Kill() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = true
	f.killCnt++
	f.exited = true
}

func (f *fakeSubprocess) crash() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exited = true
}

func (f *fakeSubprocess) wasKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

// fakeGateway is a no-op gatewayClient that records calls so tests can
// assert event dispatch + Configure happened. Errors can be injected
// via configureErr / configureCalls.
type fakeGateway struct {
	mu             sync.Mutex
	configureErr   error
	configureCalls int
	requested      []string
	resolved       []string
	shutdowns      int
}

func (g *fakeGateway) Configure(ctx context.Context, settings map[string]string) error {
	g.mu.Lock()
	g.configureCalls++
	err := g.configureErr
	g.mu.Unlock()
	return err
}

func (g *fakeGateway) OnHumanInputRequested(ctx context.Context, rec gwsdk.HumanInputRecord) error {
	g.mu.Lock()
	g.requested = append(g.requested, rec.ToolCallID)
	g.mu.Unlock()
	return nil
}

func (g *fakeGateway) OnHumanInputResolved(ctx context.Context, rec gwsdk.HumanInputRecord) error {
	g.mu.Lock()
	g.resolved = append(g.resolved, rec.ToolCallID)
	g.mu.Unlock()
	return nil
}

func (g *fakeGateway) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.shutdowns++
	g.mu.Unlock()
	return nil
}

func (g *fakeGateway) snapshot() (cfg int, req, res []string, sd int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.configureCalls, append([]string(nil), g.requested...), append([]string(nil), g.resolved...), g.shutdowns
}

// scriptedLauncher returns a sequence of (gateway, subprocess) pairs in
// order. The Nth call to launch returns the Nth scripted result. Tests
// supply enough entries to cover the initial start plus expected
// restarts.
type scriptedLauncher struct {
	mu      sync.Mutex
	calls   int32
	results []launchResult
	// launchErrs, when index-aligned, makes the Nth call return an error
	// instead of the scripted result.
	launchErrs []error
}

type launchResult struct {
	gw   *fakeGateway
	proc *fakeSubprocess
}

func (s *scriptedLauncher) launcher() launcher {
	return func(ctx context.Context, cfg Config, host *gwsdk.HostPlugin) (gatewayClient, subprocess, error) {
		idx := int(atomic.AddInt32(&s.calls, 1) - 1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if idx < len(s.launchErrs) && s.launchErrs[idx] != nil {
			return nil, nil, s.launchErrs[idx]
		}
		if idx >= len(s.results) {
			return nil, nil, errors.New("scriptedLauncher: ran out of scripted results")
		}
		r := s.results[idx]
		return r.gw, r.proc, nil
	}
}

func (s *scriptedLauncher) callCount() int {
	return int(atomic.LoadInt32(&s.calls))
}

func newTestBundle() *store.Bundle {
	dir := GinkgoT().TempDir()
	bundle, err := store.NewSQLiteBundle(filepath.Join(dir, "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(bundle.Close)
	return bundle
}

func newTestManager(launch launcher) *Manager {
	m := NewManager(newTestBundle(), humaninput.New(), nil)
	m.launch = launch
	// Speed up watchdog for tests so we don't pay 2s per crash.
	m.watchdogInterval = 20 * time.Millisecond
	m.initialBackoff = 5 * time.Millisecond
	m.maxBackoff = 50 * time.Millisecond
	return m
}

var _ = Describe("Manager.Start / Stop", func() {
	It("launches and configures the gateway with the supplied settings", func() {
		gw := &fakeGateway{}
		proc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{gw, proc}}}

		m := newTestManager(s.launcher())
		err := m.Start(context.Background(), Config{
			Name:     "discord",
			Version:  "local",
			Settings: map[string]string{"bot_token": "xyz"},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(m.Stop)

		cfgCalls, _, _, _ := gw.snapshot()
		Expect(cfgCalls).To(Equal(1), "Configure should be called exactly once on startup")
		Expect(s.callCount()).To(Equal(1))
	})

	It("kills the subprocess and propagates the error if Configure fails", func() {
		gw := &fakeGateway{configureErr: errors.New("missing setting: bot_token")}
		proc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{gw, proc}}}

		m := newTestManager(s.launcher())
		err := m.Start(context.Background(), Config{Name: "discord", Version: "local"})
		Expect(err).To(MatchError(ContainSubstring("missing setting: bot_token")))
		Expect(proc.wasKilled()).To(BeTrue(), "Configure failure must kill the subprocess so it doesn't linger")
	})

	It("is idempotent on re-Start with the same Config", func() {
		gw := &fakeGateway{}
		proc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{gw, proc}}}

		m := newTestManager(s.launcher())
		cfg := Config{Name: "discord", Version: "local", Settings: map[string]string{"k": "v"}}
		Expect(m.Start(context.Background(), cfg)).To(Succeed())
		Expect(m.Start(context.Background(), cfg)).To(Succeed())
		DeferCleanup(m.Stop)

		Expect(s.callCount()).To(Equal(1), "second Start with identical Config must not relaunch")
	})

	It("replaces the running gateway when Start is called with a different Config", func() {
		first := &fakeGateway{}
		firstProc := &fakeSubprocess{}
		second := &fakeGateway{}
		secondProc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{first, firstProc}, {second, secondProc}}}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "v1"})).To(Succeed())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "v2"})).To(Succeed())
		DeferCleanup(m.Stop)

		Expect(firstProc.wasKilled()).To(BeTrue(), "the v1 subprocess must be killed before v2 launches")
		Expect(s.callCount()).To(Equal(2))
	})

	It("Stop kills the subprocess and is safe to call twice", func() {
		gw := &fakeGateway{}
		proc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{gw, proc}}}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "local"})).To(Succeed())

		m.Stop()
		Expect(proc.wasKilled()).To(BeTrue())
		Expect(func() { m.Stop() }).NotTo(Panic())
	})
})

var _ = Describe("Manager.watchdog", func() {
	It("restarts the gateway after the subprocess crashes", func() {
		first := &fakeGateway{}
		firstProc := &fakeSubprocess{}
		second := &fakeGateway{}
		secondProc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{first, firstProc}, {second, secondProc}}}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "local"})).To(Succeed())
		DeferCleanup(m.Stop)

		// Simulate a crash.
		firstProc.crash()

		// Watchdog should observe the crash and re-launch within a few ticks.
		Eventually(s.callCount, time.Second, 10*time.Millisecond).Should(Equal(2))
		Eventually(func() int {
			cfgCalls, _, _, _ := second.snapshot()
			return cfgCalls
		}, time.Second, 10*time.Millisecond).Should(Equal(1), "the replacement gateway must be Configured")
	})

	It("keeps retrying with backoff when restart Configure fails repeatedly", func() {
		// First crash → second launch returns a Configure error → third launch succeeds.
		first := &fakeGateway{}
		firstProc := &fakeSubprocess{}
		second := &fakeGateway{configureErr: errors.New("transient configure failure")}
		secondProc := &fakeSubprocess{}
		third := &fakeGateway{}
		thirdProc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{
			{first, firstProc},
			{second, secondProc},
			{third, thirdProc},
		}}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "local"})).To(Succeed())
		DeferCleanup(m.Stop)

		firstProc.crash()

		// Second launch hits Configure error → its proc is killed mid-restart.
		Eventually(secondProc.wasKilled, time.Second, 10*time.Millisecond).Should(BeTrue())
		// Third launch should eventually succeed despite backoff.
		Eventually(func() int {
			cfgCalls, _, _, _ := third.snapshot()
			return cfgCalls
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(1))
	})

	It("does NOT restart after Stop is called — Stop is the only way to kill the gateway permanently", func() {
		gw := &fakeGateway{}
		proc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{gw, proc}}}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "local"})).To(Succeed())

		m.Stop()
		Expect(s.callCount()).To(Equal(1))

		// Give the watchdog ample time to (incorrectly) try a restart.
		// 5× the interval is enough for several poll loops.
		time.Sleep(5 * m.watchdogInterval)
		Expect(s.callCount()).To(Equal(1), "no restart should happen after Stop, even if subprocess.Exited() is true")
	})

	It("restarts more than once when the gateway keeps dying", func() {
		// 4 launches: each subprocess crashes immediately; the 4th survives.
		results := make([]launchResult, 4)
		for i := range results {
			results[i] = launchResult{&fakeGateway{}, &fakeSubprocess{}}
		}
		s := &scriptedLauncher{results: results}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "local"})).To(Succeed())
		DeferCleanup(m.Stop)

		// Crash the first 3 subprocesses; the watchdog will replace each.
		results[0].proc.crash()
		Eventually(s.callCount, time.Second, 10*time.Millisecond).Should(BeNumerically(">=", 2))
		results[1].proc.crash()
		Eventually(s.callCount, time.Second, 10*time.Millisecond).Should(BeNumerically(">=", 3))
		results[2].proc.crash()
		Eventually(s.callCount, time.Second, 10*time.Millisecond).Should(Equal(4))

		// Final survivor must be the one Configured last.
		Eventually(func() int {
			cfgCalls, _, _, _ := results[3].gw.snapshot()
			return cfgCalls
		}, time.Second, 10*time.Millisecond).Should(Equal(1))
	})
})

var _ = Describe("Manager event dispatch", func() {
	It("forwards humaninput Created and Resolved events to the gateway", func() {
		gw := &fakeGateway{}
		proc := &fakeSubprocess{}
		s := &scriptedLauncher{results: []launchResult{{gw, proc}}}

		m := newTestManager(s.launcher())
		Expect(m.Start(context.Background(), Config{Name: "discord", Version: "local"})).To(Succeed())
		DeferCleanup(m.Stop)

		m.notifier.Publish(humaninput.Event{
			Kind:   humaninput.EventKindCreated,
			Record: store.HumanInputRequestRecord{ToolCallID: "tc-1"},
		})
		m.notifier.Publish(humaninput.Event{
			Kind:   humaninput.EventKindResolved,
			Record: store.HumanInputRequestRecord{ToolCallID: "tc-2"},
		})

		Eventually(func() []string {
			_, req, _, _ := gw.snapshot()
			return req
		}, time.Second, 10*time.Millisecond).Should(ConsistOf("tc-1"))
		Eventually(func() []string {
			_, _, res, _ := gw.snapshot()
			return res
		}, time.Second, 10*time.Millisecond).Should(ConsistOf("tc-2"))
	})
})
