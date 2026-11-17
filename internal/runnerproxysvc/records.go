package runnerproxysvc

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// BlockedLine is the phrase that starts each blocked line of the job log.
const BlockedLine = "blocked by the job proxy"

// Byte values that a log field must not hold.
const (
	firstPrintable = 0x20
	asciiDelete    = 0x7f
)

// LogSink writes the blocked line of each refused request to a job log. It
// writes no line for an allowed request. A line holds the host, the
// canonical path and the fixed reason, and no header, body, query value or
// credential.
type LogSink struct {
	mu  sync.Mutex
	out io.Writer
}

// NewLogSink returns a sink that writes to out.
func NewLogSink(out io.Writer) *LogSink {
	return &LogSink{mu: sync.Mutex{}, out: out}
}

// Write writes one blocked line for a refused record, and nothing for an
// allowed one. It returns ErrLog when the job log does not take the line.
func (s *LogSink) Write(rec proxy.Record) error {
	if rec.Allowed {
		return nil
	}

	path := ""
	if canonical, ok := jobproxy.CanonicalPath(rec.Path); ok {
		path = canonical
	}

	reason := cleanField(string(rec.Reason))
	if reason == "" {
		reason = "refused"
	}

	line := fmt.Sprintf("%s: host=%s path=%s reason=%s\n",
		BlockedLine, cleanField(rec.Host), path, reason)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.out == nil {
		return fmt.Errorf("%w: no writer", ErrLog)
	}

	if _, err := io.WriteString(s.out, line); err != nil {
		return fmt.Errorf("%w: %w", ErrLog, err)
	}

	return nil
}

// cleanField removes each control character, so a value cannot add a line to
// the job log.
func cleanField(value string) string {
	return strings.Map(func(r rune) rune {
		if r < firstPrintable || r == asciiDelete {
			return -1
		}

		return r
	}, value)
}
