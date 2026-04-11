package vault

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"squadron/internal/paths"
)

// FileProvider stores the vault passphrase as a plain file at
// SQUADRON_HOME/vault.key with 0600 permissions. This is the default
// provider: the variable values are still encrypted at rest, but the
// key sits next to the vault and requires no OS keychain interaction.
type FileProvider struct{}

func (p *FileProvider) Name() string { return ProviderFile }

func (p *FileProvider) keyPath() (string, error) {
	home, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, KeyFileName), nil
}

func (p *FileProvider) Resolve() ([]byte, error) {
	path, err := p.keyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading vault key file: %w", err)
	}
	// Trim any trailing newline the user might have added when
	// writing the file by hand, but leave binary content intact.
	return bytes.TrimRight(data, "\n\r"), nil
}

func (p *FileProvider) Store(passphrase []byte) error {
	if err := paths.EnsureHome(); err != nil {
		return err
	}
	path, err := p.keyPath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, passphrase, 0600); err != nil {
		return fmt.Errorf("writing vault key file: %w", err)
	}
	return nil
}

func (p *FileProvider) Clear() error {
	path, err := p.keyPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing vault key file: %w", err)
	}
	return nil
}
