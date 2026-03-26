package vault

const (
	// File format
	Magic        = "SQVAULT1"
	Version byte = 0x01
	MagicLen     = 8
	VersionLen   = 1
	SaltLen      = 16
	NonceLen     = 12
	HeaderLen    = MagicLen + VersionLen + SaltLen + NonceLen // 37 bytes

	// Argon2id parameters (OWASP recommended)
	Argon2Time    = 3
	Argon2Memory  = 64 * 1024 // 64 MB
	Argon2Threads = 4
	Argon2KeyLen  = 32 // 256 bits

	// Keyring
	KeyringService = "squadron"
	KeyringKey     = "vault-passphrase"

	// Well-known passphrase file path (Docker secrets)
	DockerSecretPath = "/run/secrets/vault_passphrase"

	// Hardcoded fallback passphrase (public, open source — encryption at rest only)
	FallbackPassphrase = "squadron-default-insecure-passphrase-v1"
)
