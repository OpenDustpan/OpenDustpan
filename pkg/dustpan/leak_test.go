package dustpan_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestMain fails the package when a test leaves a goroutine running, or a
// schema on the PostgreSQL server.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(schemaCheck{m: m})
}

// schemaCheck runs the tests and then lists the schemas that the tests of the
// package made and did not drop.
type schemaCheck struct {
	m *testing.M
}

// Run implements goleak.TestingM.
func (c schemaCheck) Run() int {
	code := c.m.Run()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if base == "" {
		return code
	}

	left, err := dustpan.LeftGroupSchemas(context.Background(), base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list the schemas that stay: %v\n", err)

		return 1
	}

	if len(left) > 0 {
		fmt.Fprintf(os.Stderr, "schemas stay after the tests: %v\n", left)

		return 1
	}

	return code
}
