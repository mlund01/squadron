package config

import (
	"strings"

	"squadron/config/kvstore"
)

// The vault-backed key-value operations live in config/kvstore. This file is
// kept as a thin compatibility wrapper so existing callers that import
// config.GetVar / config.SetVar / config.LoadVarsFromFile continue to work
// unchanged, and so lower-level packages (mcp/oauth in particular) can reach
// the same storage without taking an import dependency on config — which
// would create a cycle through the MCP/plugin loaders.

// GetVarsFilePath returns the path of the legacy plaintext vars file.
func GetVarsFilePath() (string, error) { return kvstore.VarsFilePath() }

// GetVaultFilePath returns the path of the encrypted vault file.
func GetVaultFilePath() (string, error) { return kvstore.VaultFilePath() }

// IsVaultInitialized reports whether the vault file exists on disk.
func IsVaultInitialized() bool { return kvstore.IsVaultInitialized() }

// SetPassphraseFile sets the passphrase file path for vault operations.
func SetPassphraseFile(path string) { kvstore.SetPassphraseFile(path) }

// LoadVarsFromFile reads the vault (or legacy plaintext file).
func LoadVarsFromFile() (map[string]string, error) { return kvstore.LoadAll() }

// LoadPlaintextVars reads a plaintext key=value file. Exported for migration.
func LoadPlaintextVars(path string) (map[string]string, error) {
	return kvstore.LoadPlaintext(path)
}

// SaveVarsToFile writes the vault (or legacy plaintext file).
func SaveVarsToFile(vars map[string]string) error { return kvstore.SaveAll(vars) }

// GetVar returns a single variable value by name.
func GetVar(name string) (string, error) { return kvstore.Get(name) }

// SetVar persists a single variable value.
func SetVar(name, value string) error { return kvstore.Set(name, value) }

// DeleteVar removes a variable entry.
func DeleteVar(name string) error { return kvstore.Delete(name) }

// ListVars returns every variable name currently stored.
func ListVars() ([]string, error) {
	vars, err := kvstore.LoadAll()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	return names, nil
}

// ResolveVariableValue returns the effective value for a variable.
// Priority: vars file > default from config.
func ResolveVariableValue(v *Variable) (string, error) {
	fileVars, err := kvstore.LoadAll()
	if err != nil {
		return "", err
	}
	if fileValue, ok := fileVars[v.Name]; ok {
		return fileValue, nil
	}
	return v.Default, nil
}

// ResolveVarRef resolves a "var.<name>" reference against the vault.
func ResolveVarRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, "var.") {
		return ref, nil
	}
	varName := strings.TrimPrefix(ref, "var.")
	return kvstore.Get(varName)
}
