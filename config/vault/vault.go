package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"
)

var (
	ErrWrongPassphrase = errors.New("wrong passphrase or corrupted vault")
	ErrCorruptVault    = errors.New("corrupted vault file")
	ErrNotInitialized  = errors.New("squadron not initialized. Run 'squadron init' or pass --init")
)

type Vault struct {
	path string
}

func Open(path string) *Vault {
	return &Vault{path: path}
}

func (v *Vault) Exists() bool {
	_, err := os.Stat(v.path)
	return err == nil
}

func (v *Vault) Load(passphrase []byte) (map[string]string, error) {
	data, err := os.ReadFile(v.path)
	if err != nil {
		return nil, fmt.Errorf("reading vault: %w", err)
	}

	if len(data) < HeaderLen {
		return nil, ErrCorruptVault
	}

	// Validate magic header
	if string(data[:MagicLen]) != Magic {
		return nil, ErrCorruptVault
	}

	// Validate version
	if data[MagicLen] != Version {
		return nil, fmt.Errorf("unsupported vault version: %d", data[MagicLen])
	}

	// Extract components
	salt := data[MagicLen+VersionLen : MagicLen+VersionLen+SaltLen]
	nonce := data[MagicLen+VersionLen+SaltLen : HeaderLen]
	ciphertext := data[HeaderLen:]

	if len(ciphertext) == 0 {
		return nil, ErrCorruptVault
	}

	// Derive key
	key := deriveKey(passphrase, salt)

	// Decrypt
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrWrongPassphrase
	}

	// Unmarshal
	vars := make(map[string]string)
	if err := json.Unmarshal(plaintext, &vars); err != nil {
		return nil, fmt.Errorf("unmarshaling vault data: %w", err)
	}

	return vars, nil
}

func (v *Vault) Save(passphrase []byte, vars map[string]string) error {
	// Marshal
	plaintext, err := json.Marshal(vars)
	if err != nil {
		return fmt.Errorf("marshaling vault data: %w", err)
	}

	// Generate random salt and nonce
	salt := make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generating salt: %w", err)
	}

	nonce := make([]byte, NonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}

	// Derive key
	key := deriveKey(passphrase, salt)

	// Encrypt
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("creating GCM: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Build file: magic + version + salt + nonce + ciphertext
	buf := make([]byte, 0, HeaderLen+len(ciphertext))
	buf = append(buf, []byte(Magic)...)
	buf = append(buf, Version)
	buf = append(buf, salt...)
	buf = append(buf, nonce...)
	buf = append(buf, ciphertext...)

	// Write atomically
	if err := os.WriteFile(v.path, buf, 0600); err != nil {
		return fmt.Errorf("writing vault: %w", err)
	}

	return nil
}

// GeneratePassphrase creates a cryptographically random 32-byte passphrase.
func GeneratePassphrase() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generating passphrase: %w", err)
	}
	return b, nil
}

func deriveKey(passphrase, salt []byte) []byte {
	return argon2.IDKey(passphrase, salt, Argon2Time, Argon2Memory, Argon2Threads, Argon2KeyLen)
}

// ZeroBytes zeroes a byte slice to clear sensitive data from memory.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// encodeUint32 writes a uint32 in big-endian format.
func encodeUint32(buf []byte, v uint32) {
	binary.BigEndian.PutUint32(buf, v)
}
