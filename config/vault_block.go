package config

// VaultConfig is the HCL `vault` block. It selects which provider
// stores the passphrase that encrypts SQUADRON_HOME/vars.vault.
//
//	vault {
//	  provider = "file"   # or "keychain"
//	}
type VaultConfig struct {
	Provider string `hcl:"provider,optional"`
}
