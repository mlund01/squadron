package cmd

import (
	"fmt"
	"os"

	"squadron/config"
	"squadron/config/vault"

	"github.com/spf13/cobra"
)

var varsCmd = &cobra.Command{
	Use:   "vars",
	Short: "Manage variables",
	Long:  `Manage encrypted variables stored in the Squadron vault.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if !config.IsVaultInitialized() {
			fmt.Fprintf(os.Stderr, "Error: Squadron not initialized. Run 'squadron init' first.\n")
			os.Exit(1)
		}
	},
}

var varsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all variables",
	Run: func(cmd *cobra.Command, args []string) {
		vars, err := config.LoadVarsFromFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(vars) == 0 {
			fmt.Println("No variables set")
			return
		}
		for name, value := range vars {
			if isSecretName(name) {
				fmt.Printf("%s=********\n", name)
			} else {
				fmt.Printf("%s=%s\n", name, value)
			}
		}
	},
}

func isSecretName(name string) bool {
	secretSuffixes := []string{"_key", "_token", "_secret", "_password", "_api_key"}
	for _, suffix := range secretSuffixes {
		if len(name) >= len(suffix) && name[len(name)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}

var varsGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get a variable value",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		value, err := config.GetVar(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(value)
	},
}

var varsSetCmd = &cobra.Command{
	Use:   "set [name] [value]",
	Short: "Set a variable value",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if err := config.SetVar(args[0], args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Variable '%s' set\n", args[0])
	},
}

var varsDeleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete a variable",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := config.DeleteVar(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Variable '%s' deleted\n", args[0])
	},
}

var varsChangePassphraseCmd = &cobra.Command{
	Use:   "change-passphrase",
	Short: "Change the vault passphrase",
	Run: func(cmd *cobra.Command, args []string) {
		// Load current vars with old passphrase
		vars, err := config.LoadVarsFromFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading vars: %v\n", err)
			os.Exit(1)
		}

		// Generate or read new passphrase
		var newPassphrase []byte
		newPPFile, _ := cmd.Flags().GetString("new-passphrase-file")
		if newPPFile != "" {
			data, err := os.ReadFile(newPPFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading new passphrase file: %v\n", err)
				os.Exit(1)
			}
			newPassphrase = data
		} else {
			newPassphrase, err = vault.GeneratePassphrase()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error generating passphrase: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Generated new passphrase.")
		}

		// Re-encrypt with new passphrase
		config.SetPassphraseFile("") // clear so we use the new one directly
		vaultPath, err := config.GetVaultFilePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		v := vault.Open(vaultPath)
		if err := v.Save(newPassphrase, vars); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving vault: %v\n", err)
			os.Exit(1)
		}

		// Update keyring — delete old entry first so the new one gets fresh ACLs (trusted app)
		_ = vault.ClearKeyringPassphrase()
		if err := vault.StorePassphrase(newPassphrase); err != nil {
			fmt.Fprintf(os.Stderr, "Note: Could not store new passphrase in keychain: %v\n", err)
		}

		// Update in-process cache
		vault.CachePassphrase(newPassphrase)

		fmt.Println("Vault passphrase changed.")
	},
}

var varsExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export variables as plaintext",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(os.Stderr, "WARNING: Output contains plaintext secrets.")
		vars, err := config.LoadVarsFromFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		for name, value := range vars {
			fmt.Printf("%s=%s\n", name, value)
		}
	},
}

func init() {
	rootCmd.AddCommand(varsCmd)
	varsCmd.AddCommand(varsListCmd)
	varsCmd.AddCommand(varsGetCmd)
	varsCmd.AddCommand(varsSetCmd)
	varsCmd.AddCommand(varsDeleteCmd)
	varsCmd.AddCommand(varsChangePassphraseCmd)
	varsCmd.AddCommand(varsExportCmd)

	varsChangePassphraseCmd.Flags().String("new-passphrase-file", "", "Path to file containing new passphrase")
}
