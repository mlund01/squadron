package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"squadron/config"
	"squadron/config/vault"
	"squadron/internal/paths"

	"github.com/spf13/cobra"
)

var (
	initPassphraseFile string
	initVaultProvider  string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Squadron (create encrypted vault for secrets)",
	Long: `Initialize a Squadron instance by creating the .squadron directory
in the current working directory and setting up an encrypted vault for
secret storage.

A cryptographically random passphrase is generated and stored via the
configured vault provider:

  file     (default) — passphrase is written to .squadron/vault.key
                       (0600 perms). No OS keychain prompts.
  keychain          — passphrase is stored in the OS keychain (macOS
                       Keychain, Linux Secret Service, Windows Cred
                       Manager). More secure at rest but triggers a
                       password / passkey prompt on first access per
                       process.

Use --passphrase-file to provide your own passphrase instead of
auto-generating one. The .squadron directory is created in the
current working directory.

If variables already exist in vars.txt, they are migrated into the vault
and vars.txt is deleted.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := RunInit(initPassphraseFile, initVaultProvider); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initPassphraseFile, "passphrase-file", "", "Path to file containing vault passphrase")
	initCmd.Flags().StringVar(&initVaultProvider, "vault-provider", vault.ProviderFile,
		fmt.Sprintf("Vault provider: %q or %q", vault.ProviderFile, vault.ProviderKeychain))
}

// RunInit performs the initialization logic. Exported so --init flag on other commands can call it.
func RunInit(passphraseFile, providerName string) error {
	if err := paths.EnsureHome(); err != nil {
		return fmt.Errorf("creating squadron home: %w", err)
	}

	vaultPath, err := config.GetVaultFilePath()
	if err != nil {
		return err
	}

	v := vault.Open(vaultPath)
	if v.Exists() {
		fmt.Println("Squadron is already initialized.")
		return nil
	}

	provider, err := vault.ProviderByName(providerName)
	if err != nil {
		return err
	}

	var passphrase []byte
	switch {
	case passphraseFile != "":
		passphrase, err = vault.ReadPassphraseFile(passphraseFile)
		if err != nil {
			return fmt.Errorf("reading passphrase file: %w", err)
		}
	default:
		if data, readErr := vault.ReadPassphraseFile(vault.DockerSecretPath); readErr == nil {
			passphrase = data
		} else {
			passphrase, err = vault.GeneratePassphrase()
			if err != nil {
				return fmt.Errorf("generating passphrase: %w", err)
			}
		}
	}
	defer vault.ZeroBytes(passphrase)

	// Best effort: a keychain backend may fail in Docker / CI.
	if storeErr := provider.Store(passphrase); storeErr != nil {
		if passphraseFile == "" {
			if _, dockerErr := os.Stat(vault.DockerSecretPath); dockerErr != nil {
				fmt.Fprintf(os.Stderr, "Note: %s provider unavailable (%v). Using default passphrase.\n", provider.Name(), storeErr)
				passphrase = []byte(vault.FallbackPassphrase)
			}
		}
	}

	// Migrate existing vars.txt if present
	vars := make(map[string]string)
	varsPath, err := config.GetVarsFilePath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(varsPath); statErr == nil {
		vars, err = config.LoadPlaintextVars(varsPath)
		if err != nil {
			return fmt.Errorf("reading existing vars.txt: %w", err)
		}
		if len(vars) > 0 {
			fmt.Printf("Migrating %d variables from vars.txt to encrypted vault...\n", len(vars))
		}
	}

	// Create vault
	if err := v.Save(passphrase, vars); err != nil {
		return fmt.Errorf("creating vault: %w", err)
	}

	// Delete old vars.txt
	if _, statErr := os.Stat(varsPath); statErr == nil {
		if err := os.Remove(varsPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not delete vars.txt: %v\n", err)
		}
	}

	// Cache for current process
	vault.CachePassphrase(passphrase)

	fmt.Printf("Squadron initialized with %q vault provider. Secrets are now encrypted at rest.\n", provider.Name())
	return nil
}

// EnsureInitialized checks that squadron has been initialized.
// If autoInit is true, runs init automatically.
func EnsureInitialized(autoInit bool) error {
	if config.IsVaultInitialized() {
		return nil
	}

	if !autoInit {
		return fmt.Errorf("squadron not initialized. Run 'squadron init' or pass --init")
	}

	fmt.Println("Auto-initializing Squadron...")
	return RunInit("", vault.ProviderFile)
}

// promptYesNo asks a yes/no question (unused for now, available for future use).
func promptYesNo(question string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s [y/N]: ", question)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}
