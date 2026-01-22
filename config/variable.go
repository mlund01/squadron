package config

import "fmt"

type Variable struct {
	Name    string `hcl:"name,label"`
	Default string `hcl:"default,optional"`
	Secret  bool   `hcl:"secret,optional"`
}

func (v *Variable) Validate() error {
	if v.Secret && v.Default != "" {
		return fmt.Errorf("Invalid secret; Secret variable '%s' cannot have a default value set in config", v.Name)
	}
	return nil
}
