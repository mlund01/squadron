package wsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mlund01/squadron-sdk/protocol"

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

// Client manages the WebSocket connection from a squadron instance to commander.
type Client struct {
	cfg        *config.Config
	configPath string
	stores     *store.Bundle
	version    string

	ws   *websocket.Conn
	send chan []byte

	mu         sync.Mutex
	pending    map[string]chan *protocol.Envelope // requestID → response channel
	instanceID string                             // assigned by commander on register

	// Incoming request handlers
	handlers map[protocol.MessageType]RequestHandler

	// Active chat sessions
	chatMu       sync.Mutex
	chatSessions map[string]*chatSession // sessionID → session

	// Lifecycle
	done chan struct{}
	ctx  context.Context
	stop context.CancelFunc
}

// chatSession holds an active chat agent.
type chatSession struct {
	agent *agent.Agent
}

// RequestHandler processes an incoming request from commander and returns a response payload.
type RequestHandler func(env *protocol.Envelope) (*protocol.Envelope, error)

// NewClient creates a new wsbridge client.
func NewClient(cfg *config.Config, configPath string, stores *store.Bundle, version string) *Client {
	ctx, stop := context.WithCancel(context.Background())
	c := &Client{
		cfg:        cfg,
		configPath: configPath,
		stores:     stores,
		version:    version,
		send:         make(chan []byte, 256),
		pending:      make(map[string]chan *protocol.Envelope),
		handlers:     make(map[protocol.MessageType]RequestHandler),
		chatSessions: make(map[string]*chatSession),
		done:         make(chan struct{}),
		ctx:        ctx,
		stop:       stop,
	}
	c.registerHandlers()
	return c
}

// Connect dials the commander WebSocket endpoint, registers, and starts read/write pumps.
func (c *Client) Connect() error {
	url := c.cfg.Commander.URL
	log.Printf("Connecting to commander at %s...", url)

	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("dial commander: %w", err)
	}
	c.ws = ws

	// Start pumps first — register() needs them to send/receive messages
	go c.readPump()
	go c.writePump()

	// Register with commander
	if err := c.register(); err != nil {
		c.Close()
		return fmt.Errorf("register: %w", err)
	}

	log.Printf("Registered with commander (instanceID=%s)", c.instanceID)
	return nil
}

// Run blocks until the connection is closed or the context is cancelled.
func (c *Client) Run() error {
	select {
	case <-c.done:
		return fmt.Errorf("connection closed")
	case <-c.ctx.Done():
		return nil
	}
}

// Close shuts down the client.
func (c *Client) Close() {
	c.stop()
	if c.ws != nil {
		c.ws.Close()
	}
}

// InstanceID returns the ID assigned by commander.
func (c *Client) InstanceID() string {
	return c.instanceID
}

func (c *Client) register() error {
	instanceConfig := ConfigToInstanceConfig(c.cfg)

	req, err := protocol.NewRequest(protocol.TypeRegister, &protocol.RegisterPayload{
		InstanceName: c.cfg.Commander.InstanceName,
		Version:      c.version,
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
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
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
