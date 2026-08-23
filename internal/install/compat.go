package install

import (
	"fmt"
	"strings"
)

func CheckCompatible(component, actual string, supported []string) error {
	for _, version := range supported {
		if strings.TrimSpace(actual) == strings.TrimSpace(version) {
			return nil
		}
	}
	return fmt.Errorf("unsupported %s version %q; supported versions: %s", component, actual, strings.Join(supported, ", "))
}
