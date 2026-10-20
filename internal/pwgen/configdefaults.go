package pwgen

import (
	"fmt"
	"strings"

	"github.com/shibukawa/tinybind-go/configbind/codegen"
	"github.com/shibukawa/tinybind-go/generator"
)

// ValidateConfigDefaults refuses list-looking defaults that configbind would
// otherwise silently split as scalar CSV, including the one-element ["[]"].
func ValidateConfigDefaults(directory string, options generator.Options) error {
	_, specs, err := generator.AnalyzeConfigBindWithOptions(directory, options)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		if err := validateStringSliceDefaults(spec.Prefix, spec.Fields); err != nil {
			return err
		}
	}
	return nil
}

func validateStringSliceDefaults(prefix string, fields []codegen.Field) error {
	for _, field := range fields {
		key := strings.Trim(prefix+"."+field.Key, ".")
		value := strings.TrimSpace(field.Default)
		if field.Kind == codegen.FieldStringSlice && strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			return fmt.Errorf("%s: []string default %q is not an array; use comma-separated values, or omit the default for an empty slice", key, field.Default)
		}
		if err := validateStringSliceDefaults(key, field.Nested); err != nil {
			return err
		}
	}
	return nil
}
