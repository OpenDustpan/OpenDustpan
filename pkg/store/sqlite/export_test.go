package sqlite //nolint:testpackage // The white-box template state is unexported, so the exporter must stay in that package.

import (
	"fmt"
	"os"
)

// RemoveInternalTemplates removes the white-box template directory built for
// this test binary. It skips removal when the template was never built.
func RemoveInternalTemplates() error {
	if internalTemplateState.dir == "" {
		return nil
	}

	if err := os.RemoveAll(internalTemplateState.dir); err != nil {
		return fmt.Errorf("remove store template: %w", err)
	}

	return nil
}
