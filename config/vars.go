package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func GetVarsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".squad", "vars.txt"), nil
}

func ensureVarsDir() error {
	path, err := GetVarsFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0700)
}

func LoadVarsFromFile() (map[string]string, error) {
	vars := make(map[string]string)

	path, err := GetVarsFilePath()
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return vars, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			vars[parts[0]] = parts[1]
		}
	}

	return vars, scanner.Err()
}

func SaveVarsToFile(vars map[string]string) error {
	if err := ensureVarsDir(); err != nil {
		return err
	}

	path, err := GetVarsFilePath()
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	for name, value := range vars {
		if _, err := fmt.Fprintf(file, "%s=%s\n", name, value); err != nil {
			return err
		}
	}

	return nil
}

func GetVar(name string) (string, error) {
	vars, err := LoadVarsFromFile()
	if err != nil {
		return "", err
	}
	value, ok := vars[name]
	if !ok {
		return "", fmt.Errorf("variable '%s' not found", name)
	}
	return value, nil
}

func SetVar(name, value string) error {
	vars, err := LoadVarsFromFile()
	if err != nil {
		return err
	}
	vars[name] = value
	return SaveVarsToFile(vars)
}

func DeleteVar(name string) error {
	vars, err := LoadVarsFromFile()
	if err != nil {
		return err
	}
	if _, ok := vars[name]; !ok {
		return fmt.Errorf("variable '%s' not found", name)
	}
	delete(vars, name)
	return SaveVarsToFile(vars)
}

func ListVars() ([]string, error) {
	vars, err := LoadVarsFromFile()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	return names, nil
}

// ResolveVariableValue returns the effective value for a variable
// Priority: vars.txt file > default from config
func ResolveVariableValue(v *Variable) (string, error) {
	fileVars, err := LoadVarsFromFile()
	if err != nil {
		return "", err
	}

	if fileValue, ok := fileVars[v.Name]; ok {
		return fileValue, nil
	}

	return v.Default, nil
}

// ResolveVarRef resolves a variable reference (e.g., "var.openai_api_key")
// Returns the resolved value from vars.txt
func ResolveVarRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, "var.") {
		return ref, nil // Not a variable reference, return as-is
	}

	varName := strings.TrimPrefix(ref, "var.")
	return GetVar(varName)
}
