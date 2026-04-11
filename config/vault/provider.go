package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"squadron/internal/paths"
)

// Provider stores and retrieves the vault passphrase.
type Provider interface {
	Name() string
	Resolve() ([]byte, error)
	Store(passphrase []byte) error
	// Clear removes any stored passphrase. Clearing a non-existent
	// value is not an error.
	Clear() error
}

// overrideName is the provider name selected by the HCL `vault` block.
// It is written once during config.LoadAndValidate (stage 0) and read
// later in the same call chain by ActiveProvider; reads and writes are
// sequential, so no synchronization is needed.
var overrideName string

// SetActiveProviderName installs the provider name from the HCL
// `vault` block. Passing "" returns to auto-detection.
func SetActiveProviderName(name string) error {
	if name != "" {
		if _, err := ProviderByName(name); err != nil {
			return err
		}
	}
	overrideName = name
	return nil
}

// ProviderByName returns a provider instance for the given name.
func ProviderByName(name string) (Provider, error) {
	switch name {
	case ProviderFile:
		return &FileProvider{}, nil
	case ProviderKeychain:
		return &KeychainProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown vault provider %q (supported: %s, %s)", name, ProviderFile, ProviderKeychain)
	}
}

// ActiveProvider returns the currently configured provider.
//
// Resolution order:
//  1. Override installed via SetActiveProviderName (HCL `vault` block)
//  2. Legacy detection: if vars.vault exists with no vault.key
//     alongside it, assume a keychain install from before the file
//     provider existed.
//  3. Default: FileProvider
func ActiveProvider() (Provider, error) {
	if overrideName != "" {
		return ProviderByName(overrideName)
	}

	home, err := paths.SquadronHome()
	if err == nil {
		_, keyErr := os.Stat(filepath.Join(home, KeyFileName))
		if os.IsNotExist(keyErr) {
			if _, vaultErr := os.Stat(filepath.Join(home, VaultFileName)); vaultErr == nil {
				return &KeychainProvider{}, nil
			}
		}
	}
	return &FileProvider{}, nil
}
