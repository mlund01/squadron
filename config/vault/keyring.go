package vault

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/99designs/keyring"
)

var (
	cachedPassphrase []byte
	cacheOnce        sync.Once
	cacheMu          sync.Mutex
)

// CachePassphrase stores a passphrase in the in-process cache.
// Used by serve mode to resolve once at startup.
func CachePassphrase(passphrase []byte) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachedPassphrase = make([]byte, len(passphrase))
	copy(cachedPassphrase, passphrase)
}

// CachedPassphrase returns the cached passphrase if available.
func CachedPassphrase() ([]byte, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachedPassphrase == nil {
		return nil, false
	}
	cp := make([]byte, len(cachedPassphrase))
	copy(cp, cachedPassphrase)
	return cp, true
}

// ResolvePassphrase resolves the vault passphrase using the following order:
// 1. In-process cache
// 2. --passphrase-file (if passphraseFile is non-empty)
// 3. /run/secrets/vault_passphrase (Docker secret)
// 4. OS keyring
// 5. Hardcoded fallback (with warning)
func ResolvePassphrase(passphraseFile string) ([]byte, error) {
	// 1. Check cache
	if p, ok := CachedPassphrase(); ok {
		return p, nil
	}

	// 2. Check --passphrase-file
	if passphraseFile != "" {
		p, err := readPassphraseFile(passphraseFile)
		if err != nil {
			return nil, fmt.Errorf("reading passphrase file: %w", err)
		}
		return p, nil
	}

	// 3. Check Docker secret path
	if p, err := readPassphraseFile(DockerSecretPath); err == nil {
		return p, nil
	}

	// 4. Check OS keyring
	if p, err := loadFromKeyring(); err == nil {
		return p, nil
	}

	// 5. Hardcoded fallback
	log.Println("WARNING: Using default passphrase. Secrets are encrypted but not secure. Run 'squadron init' or provide --passphrase-file for full protection.")
	return []byte(FallbackPassphrase), nil
}

// StorePassphrase saves the passphrase to the OS keyring.
func StorePassphrase(passphrase []byte) error {
	ring, err := openKeyring()
	if err != nil {
		return fmt.Errorf("opening keyring: %w", err)
	}

	err = ring.Set(keyring.Item{
		Key:         KeyringKey,
		Label:       "Squadron Vault Passphrase",
		Description: "Encryption passphrase for Squadron's variable vault",
		Data:        passphrase,
	})
	if err != nil {
		return fmt.Errorf("storing passphrase in keyring: %w", err)
	}

	return nil
}

// ClearKeyringPassphrase removes the passphrase from the OS keyring.
func ClearKeyringPassphrase() error {
	ring, err := openKeyring()
	if err != nil {
		return fmt.Errorf("opening keyring: %w", err)
	}

	err = ring.Remove(KeyringKey)
	if err != nil {
		return fmt.Errorf("removing passphrase from keyring: %w", err)
	}

	return nil
}

func readPassphraseFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Trim whitespace/newlines
	return []byte(strings.TrimSpace(string(data))), nil
}

func loadFromKeyring() ([]byte, error) {
	ring, err := openKeyring()
	if err != nil {
		return nil, err
	}

	item, err := ring.Get(KeyringKey)
	if err != nil {
		return nil, err
	}

	return item.Data, nil
}

func openKeyring() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName:              KeyringService,
		KeychainTrustApplication: true,
		AllowedBackends: []keyring.BackendType{
			keyring.KeychainBackend,      // macOS
			keyring.SecretServiceBackend, // Linux (D-Bus)
			keyring.KeyCtlBackend,        // Linux (kernel keyring)
			keyring.WinCredBackend,       // Windows
		},
	})
}
