package vault

import (
	"fmt"

	"github.com/99designs/keyring"
)

// KeychainProvider stores the vault passphrase in the OS keychain
// (macOS Keychain, Linux Secret Service / KeyCtl, Windows Credential
// Manager). This was the original default. It is more secure than the
// file provider at rest but triggers a password / passkey prompt on
// every fresh process.
type KeychainProvider struct{}

func (p *KeychainProvider) Name() string { return ProviderKeychain }

func (p *KeychainProvider) Resolve() ([]byte, error) {
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

func (p *KeychainProvider) Store(passphrase []byte) error {
	ring, err := openKeyring()
	if err != nil {
		return fmt.Errorf("opening keyring: %w", err)
	}
	if err := ring.Set(keyring.Item{
		Key:         KeyringKey,
		Label:       "Squadron Vault Passphrase",
		Description: "Encryption passphrase for Squadron's variable vault",
		Data:        passphrase,
	}); err != nil {
		return fmt.Errorf("storing passphrase in keyring: %w", err)
	}
	return nil
}

func (p *KeychainProvider) Clear() error {
	ring, err := openKeyring()
	if err != nil {
		return fmt.Errorf("opening keyring: %w", err)
	}
	if err := ring.Remove(KeyringKey); err != nil {
		// Treat missing entries as a no-op to match FileProvider semantics.
		if err == keyring.ErrKeyNotFound {
			return nil
		}
		return fmt.Errorf("removing passphrase from keyring: %w", err)
	}
	return nil
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
