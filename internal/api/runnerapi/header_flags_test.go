package runnerapi_test

import (
	"testing"
)

// TestHeaderFlagsMatchTheHandler proves that the runnerapi adapter describes no
// operation yet. The first operation that enters the description fails this
// test, so it cannot enter without a header scenario that checks ETag,
// If-Match and Idempotency-Key.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	ops := newEnv(t).handler.Operations()
	if len(ops) > 0 {
		t.Fatalf("the handler describes %d operations and the test has no header scenario: "+
			"add a scenario for each, as internal/api/jobs/header_flags_test.go does", len(ops))
	}
}
