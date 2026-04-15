// Package oauth is the squadron side of the MCP OAuth 2.1 flow. It provides
// a vault-backed transport.TokenStore and the interactive login orchestrator
// used by `squadron mcp login`. mcp-go owns the protocol; this package owns
// where tokens live.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"squadron/config/kvstore"
)

const (
	vaultKeyPrefix  = "oauth:"
	tokenKeySuffix  = ":token"
	clientKeySuffix = ":client"
)

// ClientCredentials is what DCR hands back; cached so relogin skips
// registration.
type ClientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// vaultMu serializes concurrent reads and writes of the OAuth key space,
// since kvstore itself offers no coordination and refreshes can race logins.
var vaultMu sync.Mutex

// VaultTokenStore implements transport.TokenStore against squadron's
// encrypted vault.
type VaultTokenStore struct {
	name string
}

func NewVaultTokenStore(name string) *VaultTokenStore {
	return &VaultTokenStore{name: name}
}

func (s *VaultTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	vaultMu.Lock()
	raw, err := kvstore.Get(tokenKeyFor(s.name))
	vaultMu.Unlock()
	if err != nil {
		// kvstore.Get can't distinguish "missing" from "vault I/O failure"
		// — return ErrNoToken so mcp-go surfaces it as needs-login rather
		// than a fatal error.
		return nil, transport.ErrNoToken
	}

	var tok transport.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("oauth %q: decoding stored token: %w", s.name, err)
	}
	return &tok, nil
}

func (s *VaultTokenStore) SaveToken(ctx context.Context, tok *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tok == nil {
		return errors.New("oauth: SaveToken called with nil token")
	}

	// Some mcp-go paths set ExpiresIn but leave ExpiresAt zero — compute it
	// so readers (status, future health check) don't need to.
	if tok.ExpiresAt.IsZero() && tok.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	blob, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("oauth %q: encoding token: %w", s.name, err)
	}

	vaultMu.Lock()
	defer vaultMu.Unlock()
	return kvstore.Set(tokenKeyFor(s.name), string(blob))
}

// DeleteToken wipes the stored token but preserves ClientCredentials so the
// next login can skip DCR. Idempotent.
func DeleteToken(name string) error {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	_ = kvstore.Delete(tokenKeyFor(name))
	return nil
}

// LoadClientCredentials returns (nil, nil) if none are stored.
func LoadClientCredentials(name string) (*ClientCredentials, error) {
	vaultMu.Lock()
	defer vaultMu.Unlock()

	raw, err := kvstore.Get(clientKeyFor(name))
	if err != nil {
		return nil, nil //nolint:nilerr // missing is a normal state
	}
	var creds ClientCredentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return nil, fmt.Errorf("oauth %q: decoding client credentials: %w", name, err)
	}
	return &creds, nil
}

func SaveClientCredentials(name string, creds ClientCredentials) error {
	blob, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("oauth %q: encoding client credentials: %w", name, err)
	}
	vaultMu.Lock()
	defer vaultMu.Unlock()
	return kvstore.Set(clientKeyFor(name), string(blob))
}

func DeleteClientCredentials(name string) error {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	_ = kvstore.Delete(clientKeyFor(name))
	return nil
}

func HasToken(name string) bool {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	_, err := kvstore.Get(tokenKeyFor(name))
	return err == nil
}

func tokenKeyFor(name string) string  { return vaultKeyPrefix + name + tokenKeySuffix }
func clientKeyFor(name string) string { return vaultKeyPrefix + name + clientKeySuffix }

// VaultSnapshot is a point-in-time copy of the OAuth key space so bulk
// inspectors (e.g. `squadron mcp status`) pay one decrypt instead of 2N.
type VaultSnapshot struct {
	entries map[string]string
}

func LoadVaultSnapshot() (*VaultSnapshot, error) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	entries, err := kvstore.LoadAll()
	if err != nil {
		return nil, err
	}
	return &VaultSnapshot{entries: entries}, nil
}

func (s *VaultSnapshot) HasToken(name string) bool {
	_, ok := s.entries[tokenKeyFor(name)]
	return ok
}

func (s *VaultSnapshot) Token(name string) (*transport.Token, error) {
	raw, ok := s.entries[tokenKeyFor(name)]
	if !ok {
		return nil, transport.ErrNoToken
	}
	var tok transport.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("oauth %q: decoding stored token: %w", name, err)
	}
	return &tok, nil
}
