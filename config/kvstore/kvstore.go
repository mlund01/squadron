// Package kvstore is the low-level key-value store that backs squadron's
// encrypted vars vault. It exists as a separate package from `config` so that
// lower-level subsystems (for example mcp/oauth, which persists OAuth tokens)
// can read and write vault entries without taking an import dependency on
// the top-level config package, which in turn imports them via the plugin
// and mcp loaders. Breaking the cycle here lets the whole graph compile.
//
// This package owns nothing about HCL config or variable resolution — those
// stay in `config`. It only knows how to take a passphrase, open the vault
// file, and read/write string values.
package kvstore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"squadron/config/vault"
	"squadron/internal/paths"
)

// passphraseFile is the optional --passphrase-file path, set by CLI commands
// that accept the flag. It mirrors the variable that used to live in config
// so the vault can be unlocked without an interactive prompt in Docker and
// CI setups.
var passphraseFile string

// SetPassphraseFile updates the process-wide passphrase file path. Called
// from cmd/root.go when --passphrase-file is supplied.
func SetPassphraseFile(path string) {
	passphraseFile = path
}

// VarsFilePath returns the path of the legacy plaintext vars file. Kept for
// backward compatibility with pre-vault installs.
func VarsFilePath() (string, error) {
	sqHome, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(sqHome, "vars.txt"), nil
}

// VaultFilePath returns the path of the encrypted vars vault.
func VaultFilePath() (string, error) {
	sqHome, err := paths.SquadronHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(sqHome, "vars.vault"), nil
}

// IsVaultInitialized reports whether the vault file exists on disk. Used by
// CLI commands to decide whether to prompt the user to run `squadron init`.
func IsVaultInitialized() bool {
	path, err := VaultFilePath()
	if err != nil {
		return false
	}
	_, statErr := os.Stat(path)
	return statErr == nil
}

// LoadAll reads every key-value pair from the vault (or the legacy plaintext
// file if the vault doesn't exist). The map returned is a fresh copy — the
// caller can mutate it without affecting the on-disk state until SaveAll is
// called with the updated map.
func LoadAll() (map[string]string, error) {
	vaultPath, err := VaultFilePath()
	if err != nil {
		return nil, err
	}

	v := vault.Open(vaultPath)
	if v.Exists() {
		passphrase, err := vault.ResolvePassphrase(passphraseFile)
		if err != nil {
			return nil, fmt.Errorf("resolving passphrase: %w", err)
		}
		defer vault.ZeroBytes(passphrase)
		return v.Load(passphrase)
	}

	// Fall back to plaintext vars.txt (legacy / pre-init).
	varsPath, err := VarsFilePath()
	if err != nil {
		return nil, err
	}
	return loadPlaintext(varsPath)
}

// LoadPlaintext reads a key=value file at the given path. Exported so the
// init command can migrate legacy files to the encrypted vault.
func LoadPlaintext(path string) (map[string]string, error) {
	return loadPlaintext(path)
}

func loadPlaintext(path string) (map[string]string, error) {
	vars := make(map[string]string)

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return vars, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			vars[parts[0]] = parts[1]
		}
	}

	return vars, scanner.Err()
}

// SaveAll replaces the entire on-disk contents with the given map. If the
// vault is initialized the write is encrypted; otherwise we fall back to
// the plaintext file for legacy compatibility.
func SaveAll(vars map[string]string) error {
	vaultPath, err := VaultFilePath()
	if err != nil {
		return err
	}

	v := vault.Open(vaultPath)
	if v.Exists() {
		passphrase, err := vault.ResolvePassphrase(passphraseFile)
		if err != nil {
			return fmt.Errorf("resolving passphrase: %w", err)
		}
		defer vault.ZeroBytes(passphrase)
		return v.Save(passphrase, vars)
	}

	if err := ensureVarsDir(); err != nil {
		return err
	}

	path, err := VarsFilePath()
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	for name, value := range vars {
		if _, err := fmt.Fprintf(file, "%s=%s\n", name, value); err != nil {
			return err
		}
	}
	return nil
}

// Get returns a single value by key, or an error if the key is missing.
func Get(name string) (string, error) {
	vars, err := LoadAll()
	if err != nil {
		return "", err
	}
	value, ok := vars[name]
	if !ok {
		return "", fmt.Errorf("variable '%s' not found", name)
	}
	return value, nil
}

// Set writes a single value. Existing entries are overwritten; missing entries
// are added. This is a read-modify-write against the whole file, so callers
// doing bulk updates should prefer SaveAll.
func Set(name, value string) error {
	vars, err := LoadAll()
	if err != nil {
		return err
	}
	vars[name] = value
	return SaveAll(vars)
}

// Delete removes a key. Returns an error if the key was not present.
func Delete(name string) error {
	vars, err := LoadAll()
	if err != nil {
		return err
	}
	if _, ok := vars[name]; !ok {
		return fmt.Errorf("variable '%s' not found", name)
	}
	delete(vars, name)
	return SaveAll(vars)
}

func ensureVarsDir() error {
	path, err := VarsFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0700)
}
