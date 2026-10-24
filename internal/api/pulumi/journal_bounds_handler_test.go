//go:build journalrecording

package pulumi_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// stallReplayer blocks until its context ends, then reports the context error.
// It drives the real build deadline path at complete.
type stallReplayer struct{}

func (stallReplayer) Replay(
	ctx context.Context, _ func(context.Context) (io.ReadCloser, error), _ [][]byte, _ io.Writer,
) error {
	<-ctx.Done()
	return fmt.Errorf("stall replayer: %w", ctx.Err())
}

// replayFixture builds an update fixture with the given journal replayer.
func replayFixture(t *testing.T, replayer stack.JournalReplayer) updateFixture {
	t.Helper()
	return newUpdateFixtureWithAuthenticator(
		t,
		allowAll(),
		tokenAuthenticator{},
		fixtureOptions{manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: replayer},
	)
}

// TestJournalCompleteAnswers413OnReplayBound checks that completing a
// journaled update past a replay bound answers 413 with a clear message.
//
//nolint:paralleltest // The test lowers the entry bound, so it runs alone.
func TestJournalCompleteAnswers413OnReplayBound(t *testing.T) {
	old := pulumijournal.MaxEntriesPerUpdate
	pulumijournal.MaxEntriesPerUpdate = 4
	t.Cleanup(func() { pulumijournal.MaxEntriesPerUpdate = old })

	f := replayFixture(t, pulumijournal.NewReplayer(time.Now, ""))
	id, lease := mintJournaled(t, f)

	const body = `{"entries":[` +
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":2,"operationID":2,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":3,"operationID":3,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":4,"operationID":4,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":5,"operationID":5,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":6,"operationID":6,"removeOld":null,"removeNew":null}]}`

	appendStatus, appendResp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, body))
	if appendStatus != http.StatusOK {
		t.Fatalf("append batch answers %d, want %d (%s)", appendStatus, http.StatusOK, appendResp)
	}

	complete := leaseRequest(http.MethodPost, mintedPaths(id).Complete, lease, `{"status":"succeeded"}`)
	status, resp := f.doUpdate(t, complete)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"complete past the entry bound answers %d, want %d (%s)",
			status,
			http.StatusRequestEntityTooLarge,
			resp,
		)
	}
	checkReplayBoundBody(t, resp, "journal entries", pulumijournal.MaxEntriesPerUpdate)
}

// TestJournalCompleteAnswers503OnBuildTimeout checks that completing a
// journaled update whose build runs past the build timeout answers 503, not
// 500, so the CLI does not get an internal error.
//
//nolint:paralleltest // The test lowers the build timeout, so it runs alone.
func TestJournalCompleteAnswers503OnBuildTimeout(t *testing.T) {
	old := stack.MaxJournalBuildDuration
	stack.MaxJournalBuildDuration = 20000000 // 20ms
	t.Cleanup(func() { stack.MaxJournalBuildDuration = old })

	f := replayFixture(t, stallReplayer{})
	id, lease := mintJournaled(t, f)

	const body = `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`

	appendStatus, appendResp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, body))
	if appendStatus != http.StatusOK {
		t.Fatalf("append batch answers %d, want %d (%s)", appendStatus, http.StatusOK, appendResp)
	}

	complete := leaseRequest(http.MethodPost, mintedPaths(id).Complete, lease, `{"status":"succeeded"}`)
	status, resp := f.doUpdate(t, complete)
	if status != http.StatusServiceUnavailable {
		t.Fatalf(
			"complete with a timed-out build answers %d, want %d (%s)",
			status,
			http.StatusServiceUnavailable,
			resp,
		)
	}
	if !strings.Contains(resp, "timed out") {
		t.Fatalf("503 body lacks a clear message: %s", resp)
	}
}
