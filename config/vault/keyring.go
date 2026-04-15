package vault

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

var (
	cachedPassphrase []byte
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
//  1. In-process cache
//  2. --passphrase-file (if passphraseFile is non-empty)
//  3. /run/secrets/vault_passphrase (Docker secret)
//  4. The active vault provider (file or keychain)
//  5. Hardcoded fallback (with warning)
func ResolvePassphrase(passphraseFile string) ([]byte, error) {
	if p, ok := CachedPassphrase(); ok {
		return p, nil
	}

	if passphraseFile != "" {
		p, err := ReadPassphraseFile(passphraseFile)
		if err != nil {
			return nil, fmt.Errorf("reading passphrase file: %w", err)
		}
		return p, nil
	}

	if p, err := ReadPassphraseFile(DockerSecretPath); err == nil {
		return p, nil
	}

	if provider, err := ActiveProvider(); err == nil {
		if p, perr := provider.Resolve(); perr == nil {
			return p, nil
		}
	}

	log.Println("WARNING: Using default passphrase. Secrets are encrypted but not secure. Run 'squadron init' or provide --passphrase-file for full protection.")
	return []byte(FallbackPassphrase), nil
}

// ReadPassphraseFile reads a passphrase from a file, trimming trailing
// whitespace so a stray newline from an editor doesn't corrupt the key.
func ReadPassphraseFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(data))), nil
}
