package runnerapi_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
)

// TestRunnerAPIDocStatesTheCancelBound proves that the numbers of the
// reference page for the renewal during a cancel are the numbers of
// runner.SessionCancelBound, runner.GraceChanges and runner.ReportTime.
func TestRunnerAPIDocStatesTheCancelBound(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile("../../../docs/reference/runner-api.md")
	if err != nil {
		t.Fatalf("read the reference page: %v", err)
	}

	want := fmt.Sprintf(
		"for %d minutes after the server records the cancel:\nthe longest grace period of a step (%d minutes)",
		int(runner.SessionCancelBound.Minutes()), int(runner.GraceChanges.Minutes()),
	)
	if !strings.Contains(string(page), want) {
		t.Errorf("the reference page does not contain %q", want)
	}

	report := fmt.Sprintf("the finish (%d minutes)", int(runner.ReportTime.Minutes()))
	if !strings.Contains(strings.Join(strings.Fields(string(page)), " "), report) {
		t.Errorf("the reference page does not contain %q", report)
	}
}
