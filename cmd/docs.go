package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"squadron/docs"

	"github.com/spf13/cobra"
)

var docsCmd = &cobra.Command{
	Use:   "docs [output-dir]",
	Short: "Dump documentation to a local folder",
	Long:  `Extract embedded documentation markdown files to a local directory.`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		outputDir := "squadron-docs"
		if len(args) > 0 {
			outputDir = args[0]
		}

		if err := extractDocs(outputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Documentation extracted to %s/\n", outputDir)
	},
}

func extractDocs(outputDir string) error {
	return fs.WalkDir(docs.DocsFS, "pages", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the root "pages" directory
		if path == "pages" {
			return nil
		}

		// Skip non-markdown files
		if !d.IsDir() && filepath.Ext(path) != ".md" {
			return nil
		}

		// Create output path (strip "pages/" prefix)
		relPath := path[len("pages/"):]
		outPath := filepath.Join(outputDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(outPath, 0755)
		}

		// Read and write file
		content, err := docs.DocsFS.ReadFile(path)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}

		return os.WriteFile(outPath, content, 0644)
	})
}

func init() {
	rootCmd.AddCommand(docsCmd)
}
