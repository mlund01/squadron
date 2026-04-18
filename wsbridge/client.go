package wsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mlund01/squadron-wire/protocol"

	"squadron/agent"
	"squadron/config"
	"squadron/store"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	requestTimeout = 30 * time.Second
)

// runningMission tracks a running mission's stop handles.
type runningMission struct {
	cancel context.CancelFunc // hard cancel (context)
	drain  func()             // graceful drain signal
}

// Client manages the WebSocket connection from a squadron instance to commander.
type Client struct {
	cfg      *config.Config // may be partial until full load succeeds
	cfgReady bool          // true when config is fully loaded and validated
	cfgError string        // non-empty when config failed to load
	cfgMu    sync.RWMutex
	configPath string
	stores     *store.Bundle
	version    string

	ws        *websocket.Conn
	send      chan []byte
	connected bool // true after successful Connect + register

	mu         sync.Mutex
	pending    map[string]chan *protocol.Envelope // requestID → response channel
	instanceID string                             // assigned by commander on register

	// Incoming request handlers
	handlers map[protocol.MessageType]RequestHandler

	// Active chat sessions
	chatMu       sync.Mutex
	chatSessions map[string]*chatSession // sessionID → session

	// Running missions (for stop/cancel)
	missionMu       sync.Mutex
	runningMissions map[string]*runningMission // missionID → mission handle

	// Event subscriptions — controls what gets sent to commander
	subscriptions *SubscriptionManager

	// Concurrency tracker for mission max_parallel enforcement
	concurrency ConcurrencyTracker

	// OAuth callback listeners — keyed by OAuth state. The WsbridgeCallbackSource
	// registers a channel per in-flight login; commander delivers callback params
	// over WS and we fan them out to the listener matching the state value.
	oauthMu        sync.Mutex
	oauthListeners map[string]chan<- OAuthCallback

	// Lifecycle
	done chan struct{}
	ctx  context.Context
	stop context.CancelFunc

	// Callback fired when config loads successfully for the first time (or reloads)
	OnConfigLoaded func(cfg *config.Config)
}

// chatSession holds an active chat agent.
type chatSession struct {
	agent *agent.Agent
}

// ConcurrencyTracker manages mission concurrency limits (max_parallel).
type ConcurrencyTracker interface {
	NotifyMissionStarted(missionName string) bool
	NotifyMissionDone(missionName string)
}

// noopConcurrency is the default tracker when none is configured — always allows.
type noopConcurrency struct{}

func (noopConcurrency) NotifyMissionStarted(string) bool { return true }
func (noopConcurrency) NotifyMissionDone(string)         {}

// RequestHandler processes an incoming request from commander and returns a response payload.
type RequestHandler func(env *protocol.Envelope) (*protocol.Envelope, error)

// NewClient creates a new wsbridge client. cfgReady indicates whether the config
// is fully loaded and validated (false = partial config with just vars/plugins).
// cfgError is the error message when config failed to load (empty if cfgReady is true).
func NewClient(cfg *config.Config, cfgReady bool, cfgError string, configPath string, stores *store.Bundle, version string) *Client {
	ctx, stop := context.WithCancel(context.Background())
	c := &Client{
		cfg:        cfg,
		cfgReady:   cfgReady,
		cfgError:   cfgError,
		configPath: configPath,
		stores:     stores,
		version:    version,
		send:         make(chan []byte, 256),
		pending:      make(map[string]chan *protocol.Envelope),
		handlers:     make(map[protocol.MessageType]RequestHandler),
		chatSessions:    make(map[string]*chatSession),
		runningMissions: make(map[string]*runningMission),
		subscriptions:   NewSubscriptionManager(),
		concurrency:     noopConcurrency{},
		oauthListeners:  make(map[string]chan<- OAuthCallback),
		done:         make(chan struct{}),
		ctx:        ctx,
		stop:       stop,
	}
	c.registerHandlers()
	return c
}

// ConnectTo dials a specific command center URL, registers, and starts read/write pumps.
// Used when the URL is known independently of config (e.g., local command center).
func (c *Client) ConnectTo(commanderURL string) error {
	return c.connectToURL(commanderURL)
}

// Connect dials the command center WebSocket endpoint from config, registers, and starts read/write pumps.
func (c *Client) Connect() error {
	cfg := c.getConfig()
	if cfg == nil || cfg.CommandCenter == nil {
		return fmt.Errorf("config not loaded")
	}
	return c.connectToURL(cfg.CommandCenter.WebSocketURL())
}

func (c *Client) connectToURL(url string) error {
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("dial command center: %w", err)
	}
	c.ws = ws
	c.done = make(chan struct{})

	// Start pumps first — register() needs them to send/receive messages
	go c.readPump()
	go c.writePump()

	// Register with commander
	if err := c.register(); err != nil {
		c.Close()
		return fmt.Errorf("register: %w", err)
	}

	c.connected = true
	return nil
}

// IsConnected returns whether the client has an active commander connection.
func (c *Client) IsConnected() bool {
	return c.connected
}

// HasConfig returns whether the client has a fully loaded and validated config.
func (c *Client) HasConfig() bool {
	c.cfgMu.RLock()
	defer c.cfgMu.RUnlock()
	return c.cfgReady
}

// Run blocks until the connection is closed or the context is cancelled.
func (c *Client) Run() error {
	if !c.connected {
		// Not connected yet — just block until context is cancelled
		<-c.ctx.Done()
		return nil
	}
	select {
	case <-c.done:
		c.connected = false
		return fmt.Errorf("connection closed")
	case <-c.ctx.Done():
		return nil
	}
}

// SetConcurrencyTracker sets the concurrency tracker used to enforce max_parallel.
func (c *Client) SetConcurrencyTracker(ct ConcurrencyTracker) {
	c.concurrency = ct
}

// Close shuts down the client.
func (c *Client) Close() {
	c.connected = false
	c.stop()
	if c.ws != nil {
		c.ws.Close()
	}
}

// InstanceID returns the ID assigned by commander.
func (c *Client) InstanceID() string {
	return c.instanceID
}

// getConfig returns the current config, safe for concurrent access.
func (c *Client) getConfig() *config.Config {
	c.cfgMu.RLock()
	defer c.cfgMu.RUnlock()
	return c.cfg
}

// GetConfig returns the current config (exported for use by serve command).
func (c *Client) GetConfig() *config.Config {
	return c.getConfig()
}

// SetConfig replaces the current config without reloading from disk.
func (c *Client) SetConfig(cfg *config.Config) {
	c.cfgMu.Lock()
	c.cfg = cfg
	c.cfgMu.Unlock()
}

// ReloadConfig re-reads the config from disk, validates it, and swaps it in.
// On failure the old config stays active. The caller (commander) updates its
// registry from the returned config — no re-register needed.
func (c *Client) ReloadConfig() error {
	newCfg, err := config.LoadAndValidate(c.configPath)
	if err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	c.cfgMu.Lock()
	oldCfg := c.cfg
	wasReady := c.cfgReady
	c.cfg = newCfg
	c.cfgReady = true
	c.cfgError = ""
	c.cfgMu.Unlock()

	// Close plugin clients that were removed in the new config
	if wasReady && oldCfg != nil {
		closeRemovedPlugins(oldCfg, newCfg)
	}

	log.Println("Config reloaded successfully")

	if c.OnConfigLoaded != nil {
		c.OnConfigLoaded(newCfg)
	}

	return nil
}

// closeRemovedPlugins shuts down plugin clients that exist in old but not in new config.
func closeRemovedPlugins(oldCfg, newCfg *config.Config) {
	newPlugins := make(map[string]bool, len(newCfg.Plugins))
	for _, p := range newCfg.Plugins {
		newPlugins[p.Name] = true
	}
	for name, client := range oldCfg.LoadedPlugins {
		if !newPlugins[name] {
			client.Close()
		}
	}
}

func (c *Client) register() error {
	cfg := c.getConfig()
	instanceConfig := ConfigToInstanceConfig(cfg) // handles nil cfg

	instanceName := ""
	if cfg != nil && cfg.CommandCenter != nil {
		instanceName = cfg.CommandCenter.InstanceName
	}
	if instanceName == "" {
		instanceName, _ = os.Hostname()
	}

	c.cfgMu.RLock()
	cfgError := c.cfgError
	c.cfgMu.RUnlock()

	req, err := protocol.NewRequest(protocol.TypeRegister, &protocol.RegisterPayload{
		InstanceName: instanceName,
		Version:      c.version,
		ConfigReady:  c.HasConfig(),
		ConfigError:  cfgError,
		Config:       instanceConfig,
	})
	if err != nil {
		return err
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return err
	}

	var ack protocol.RegisterAckPayload
	if err := protocol.DecodePayload(resp, &ack); err != nil {
		return fmt.Errorf("decode register ack: %w", err)
	}

	if !ack.Accepted {
		return fmt.Errorf("registration rejected: %s", ack.Reason)
	}

	c.instanceID = ack.InstanceID
	return nil
}

func (c *Client) readPump() {
	defer func() {
		close(c.done)
		c.ws.Close()
	}()

	c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.ws.ReadMessage()
		if err != nil {
			if c.connected && websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(message, &env); err != nil {
			log.Printf("Invalid message from commander: %v", err)
			continue
		}

		c.dispatch(&env)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.ws.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) dispatch(env *protocol.Envelope) {
	// Check if this is a response to a pending request
	if env.RequestID != "" {
		c.mu.Lock()
		ch, ok := c.pending[env.RequestID]
		c.mu.Unlock()
		if ok {
			ch <- env
			return
		}
	}

	// Handlers that work without a loaded config
	configFreeHandlers := map[protocol.MessageType]bool{
		protocol.TypeReloadConfig:    true,
		protocol.TypeGetConfig:       true,
		protocol.TypeSetVariable:     true,
		protocol.TypeDeleteVariable:  true,
		protocol.TypeGetVariables:    true,
		protocol.TypeListConfigFiles: true,
		protocol.TypeGetConfigFile:   true,
		protocol.TypeWriteConfigFile: true,
		protocol.TypeValidateConfig:  true,
	}

	// Handle incoming requests from commander
	switch env.Type {
	case protocol.TypeHeartbeat:
		ack, _ := protocol.NewResponse(env.RequestID, protocol.TypeHeartbeatAck, &protocol.HeartbeatAckPayload{})
		c.sendEnvelope(ack)
	default:
		handler, ok := c.handlers[env.Type]
		if !ok {
			log.Printf("Unhandled message type from commander: %s", env.Type)
			return
		}
		// Guard: most handlers need a fully loaded config
		if !c.HasConfig() && !configFreeHandlers[env.Type] {
			errResp, _ := protocol.NewError(env.RequestID, "config_not_ready", "configuration is not loaded yet — set required variables or fix config errors")
			c.sendEnvelope(errResp)
			return
		}
		resp, err := handler(env)
		if err != nil {
			errResp, _ := protocol.NewError(env.RequestID, "handler_error", err.Error())
			c.sendEnvelope(errResp)
			return
		}
		if resp != nil {
			c.sendEnvelope(resp)
		}
	}
}

func (c *Client) sendEnvelope(env *protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.send <- data
	return nil
}

// SendEvent sends a one-way event to commander (no response expected).
func (c *Client) SendEvent(env *protocol.Envelope) error {
	return c.sendEnvelope(env)
}

// SendRequest sends a request to commander and waits for the correlated
// response. Exposed so packages outside wsbridge (e.g. mcp/oauth) can issue
// their own request/response round-trips.
func (c *Client) SendRequest(env *protocol.Envelope) (*protocol.Envelope, error) {
	return c.sendRequest(env)
}

// OAuthCallback is the decoded callback delivery payload pushed from commander
// into a registered listener.
type OAuthCallback struct {
	Code  string
	State string
	Error string
}

// RegisterOAuthListener attaches a channel that receives the OAuth callback
// for the given state value. Returns a cancel function to deregister. The
// buffered channel must have capacity >= 1 so we never block while holding
// the mutex.
func (c *Client) RegisterOAuthListener(state string, ch chan<- OAuthCallback) func() {
	c.oauthMu.Lock()
	c.oauthListeners[state] = ch
	c.oauthMu.Unlock()
	return func() {
		c.oauthMu.Lock()
		delete(c.oauthListeners, state)
		c.oauthMu.Unlock()
	}
}

// deliverOAuthCallback forwards a callback delivery to the registered
// listener for the given state, if any. Returns true if a listener consumed
// the delivery.
func (c *Client) deliverOAuthCallback(cb OAuthCallback) bool {
	c.oauthMu.Lock()
	ch, ok := c.oauthListeners[cb.State]
	c.oauthMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- cb:
		return true
	default:
		return false
	}
}

func (c *Client) sendRequest(env *protocol.Envelope) (*protocol.Envelope, error) {
	ch := make(chan *protocol.Envelope, 1)

	c.mu.Lock()
	c.pending[env.RequestID] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, env.RequestID)
		c.mu.Unlock()
	}()

	if err := c.sendEnvelope(env); err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(requestTimeout):
		return nil, fmt.Errorf("request timed out")
	}
}
