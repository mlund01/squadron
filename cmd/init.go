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

var initPassphraseFile string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Squadron (create encrypted vault for secrets)",
	Long: `Initialize a Squadron instance by creating the SQUADRON_HOME directory
and setting up an encrypted vault for secret storage.

A cryptographically random passphrase is generated and stored in your
OS keychain. Use --passphrase-file to provide your own passphrase instead.

If variables already exist in vars.txt, they are migrated into the vault
and vars.txt is deleted.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := RunInit(initPassphraseFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initPassphraseFile, "passphrase-file", "", "Path to file containing vault passphrase")
}

// RunInit performs the initialization logic. Exported so --init flag on other commands can call it.
func RunInit(passphraseFile string) error {
	// Ensure home directory exists
	if err := paths.EnsureHome(); err != nil {
		return fmt.Errorf("creating squadron home: %w", err)
	}

	vaultPath, err := config.GetVaultFilePath()
	if err != nil {
		return err
	}

	v := vault.Open(vaultPath)

	// Already initialized
	if v.Exists() {
		fmt.Println("Squadron is already initialized.")
		return nil
	}

	// Resolve passphrase
	var passphrase []byte
	if passphraseFile != "" {
		// Read from provided file
		passphrase, err = os.ReadFile(passphraseFile)
		if err != nil {
			return fmt.Errorf("reading passphrase file: %w", err)
		}
		passphrase = []byte(strings.TrimSpace(string(passphrase)))
	} else {
		// Check Docker secret path
		if data, readErr := os.ReadFile(vault.DockerSecretPath); readErr == nil {
			passphrase = []byte(strings.TrimSpace(string(data)))
		} else {
			// Auto-generate
			passphrase, err = vault.GeneratePassphrase()
			if err != nil {
				return fmt.Errorf("generating passphrase: %w", err)
			}
		}
	}
	defer vault.ZeroBytes(passphrase)

	// Store in keyring (best effort — may fail in Docker/CI)
	if storeErr := vault.StorePassphrase(passphrase); storeErr != nil {
		// If we auto-generated and can't persist it anywhere, fall back to
		// the hardcoded passphrase so other processes can also resolve it.
		if passphraseFile == "" {
			if _, dockerErr := os.Stat(vault.DockerSecretPath); dockerErr != nil {
				fmt.Fprintf(os.Stderr, "Note: No keychain available. Using default passphrase.\n")
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

	fmt.Println("Squadron initialized. Secrets are now encrypted at rest.")
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
	return RunInit("")
}

// promptYesNo asks a yes/no question (unused for now, available for future use).
func promptYesNo(question string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s [y/N]: ", question)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}
