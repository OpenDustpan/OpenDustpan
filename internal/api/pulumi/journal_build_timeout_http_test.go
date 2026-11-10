package pulumi_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// buildTimeoutStallReplayer blocks until its context ends, then reports the
// context error. It drives the real build deadline path at complete.
type buildTimeoutStallReplayer struct{}

func (buildTimeoutStallReplayer) Replay(
	ctx context.Context, _ func(context.Context) (io.ReadCloser, error), _ [][]byte, _ io.Writer,
) error {
	<-ctx.Done()
	return fmt.Errorf("stall replayer: %w", ctx.Err())
}

// TestJournalCompleteAnswers503OnBuildTimeoutHTTP checks that completing a
// journaled update whose build runs past the build timeout answers 503, not
// 500, so the CLI does not get an internal error.
//
//nolint:paralleltest // The test lowers the build timeout, so it runs alone.
func TestJournalCompleteAnswers503OnBuildTimeoutHTTP(t *testing.T) {
	old := stack.MaxJournalBuildDuration
	stack.MaxJournalBuildDuration = 20000000 // 20ms
	t.Cleanup(func() { stack.MaxJournalBuildDuration = old })

	f := newUpdateFixtureWithAuthenticator(
		t,
		allowAll(),
		tokenAuthenticator{},
		fixtureOptions{
			manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: buildTimeoutStallReplayer{}, guard: nil,
		},
	)

	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.StartWithJournalVersion(t.Context(), f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	id, lease := u.ID, started.Token.Reveal()

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
