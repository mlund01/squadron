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
		printed := 0
		for name, value := range vars {
			if isInternalKey(name) {
				continue
			}
			printed++
			if isSecretName(name) {
				fmt.Printf("%s=********\n", name)
			} else {
				fmt.Printf("%s=%s\n", name, value)
			}
		}
		if printed == 0 {
			fmt.Println("No variables set")
		}
	},
}

func isInternalKey(name string) bool {
	return len(name) >= 6 && name[:6] == "oauth:"
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
		if isInternalKey(args[0]) {
			fmt.Fprintf(os.Stderr, "Error: variable '%s' not found\n", args[0])
			os.Exit(1)
		}
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
	Short: "Change the vault passphrase (optionally switch provider)",
	Run: func(cmd *cobra.Command, args []string) {
		// Load current vars with old passphrase
		vars, err := config.LoadVarsFromFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading vars: %v\n", err)
			os.Exit(1)
		}

		// Determine target provider — defaults to the currently active one.
		oldProvider, err := vault.ActiveProvider()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		newProviderName, _ := cmd.Flags().GetString("provider")
		if newProviderName == "" {
			newProviderName = oldProvider.Name()
		}
		newProvider, err := vault.ProviderByName(newProviderName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		var newPassphrase []byte
		newPPFile, _ := cmd.Flags().GetString("new-passphrase-file")
		if newPPFile != "" {
			newPassphrase, err = vault.ReadPassphraseFile(newPPFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading new passphrase file: %v\n", err)
				os.Exit(1)
			}
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

		// Clear the target backend first so the keychain provider
		// reissues a fresh ACL on the trusted-app entry.
		_ = newProvider.Clear()
		if err := newProvider.Store(newPassphrase); err != nil {
			fmt.Fprintf(os.Stderr, "Note: Could not store new passphrase via %s provider: %v\n", newProvider.Name(), err)
		}

		if newProvider.Name() != oldProvider.Name() {
			if err := oldProvider.Clear(); err != nil {
				fmt.Fprintf(os.Stderr, "Note: Could not clear old %s provider: %v\n", oldProvider.Name(), err)
			}
			fmt.Printf("Switched vault provider: %s -> %s\n", oldProvider.Name(), newProvider.Name())
			fmt.Fprintf(os.Stderr, "Remember to update your HCL config's `vault` block: provider = %q\n", newProvider.Name())
		}

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
			if isInternalKey(name) {
				continue
			}
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
	varsChangePassphraseCmd.Flags().String("provider", "",
		fmt.Sprintf("Switch to a different vault provider: %q or %q", vault.ProviderFile, vault.ProviderKeychain))
}
