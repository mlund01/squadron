package cmd

import (
	"fmt"
	"os"

	"squad/config"

	"github.com/spf13/cobra"
)

var varsCmd = &cobra.Command{
	Use:   "vars",
	Short: "Manage variables",
	Long:  `Manage variables stored in ~/.squad/vars.txt`,
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

func init() {
	rootCmd.AddCommand(varsCmd)
	varsCmd.AddCommand(varsListCmd)
	varsCmd.AddCommand(varsGetCmd)
	varsCmd.AddCommand(varsSetCmd)
	varsCmd.AddCommand(varsDeleteCmd)
}
