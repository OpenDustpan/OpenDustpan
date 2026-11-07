package jobtest

import (
	"testing"
	"time"
)

// LogTimes are the times that the recorder stored with a log version.
type LogTimes struct {
	CreatedAt time.Time
	ExpiresAt time.Time
}

// LogExpiry writes one chunk of a job log through h.Recorder and answers the
// creation time and the expiry time that the recorder stored with the log
// version. A test that builds the harness with a retention period shows with
// it that the period is the one the recorder applies: the expiry is the
// creation time plus h.Retention.JobLog.
func LogExpiry(t *testing.T, h RecorderHarness) LogTimes {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "first line\n")

	log := logOf(t, h, a)

	return LogTimes{CreatedAt: log.CreatedAt, ExpiresAt: log.ExpiresAt}
}
