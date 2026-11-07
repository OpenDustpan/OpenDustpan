package jobs_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

const hiddenJobs = 2100

// scanLimitRows returns many jobs of a unit that the caller may not read, and
// then one job that the caller may read.
func scanLimitRows(t *testing.T) []job.Job {
	t.Helper()

	rows := make([]job.Job, 0, hiddenJobs+1)
	for i := range hiddenJobs {
		rows = append(rows, listedJob(t, "acme", fmt.Sprintf("job-h%019d", i), "unt-hidden"))
	}

	return append(rows, listedJob(t, "acme", "job-vvvvvvvvvvvvvvvvvvvv", "unt-a"))
}

// pagedHandler is a list handler over rows whose store honors After and Limit.
func pagedHandler(t *testing.T, rows []job.Job, cursors secret.Wrapper) testHandler {
	t.Helper()

	jobs := newFakeJobs()
	jobs.listed = func(f job.Filter) []job.Job {
		start := 0

		if f.After != "" {
			for i := range rows {
				if rows[i].ID == f.After {
					start = i + 1
				}
			}
		}

		end := min(start+f.Limit, len(rows))

		return rows[start:end]
	}

	return newWorkerHandler(t, newAuthorizer(readsUnits("unt-a")...), handlerDeps{
		jobs: jobs, starter: nil, entries: nil, artifacts: nil, logs: nil, cursors: cursors,
	}, nil)
}

func testWrapper(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	w, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	return w
}

// TestListScanLimitTokenNamesNoJob proves that the page token that the scan
// limit gives holds no ID of a job that the caller may not read.
func TestListScanLimitTokenNamesNoJob(t *testing.T) {
	t.Parallel()

	h := pagedHandler(t, scanLimitRows(t), testWrapper(t))

	code, first := getList(t, h, "")
	if code != http.StatusOK || len(first.Jobs) != 0 || first.NextPageToken == "" {
		t.Fatalf("first page = %d %v token %q, want an empty page with a token", code, ids(first), first.NextPageToken)
	}

	raw := decodeToken(t, first.NextPageToken)
	if strings.Contains(string(raw), "job-") || strings.Contains(string(rawCursor(t, raw)), "job-") {
		t.Errorf("the token names a job: %s", raw)
	}

	code, second := getList(t, h, "?pageToken="+url.QueryEscape(first.NextPageToken))
	if code != http.StatusOK || len(second.Jobs) != 1 || second.Jobs[0].ID != "job-vvvvvvvvvvvvvvvvvvvv" ||
		second.NextPageToken != "" {
		t.Errorf("second page = %d %v token %q", code, ids(second), second.NextPageToken)
	}

	checkRefusedTokens(t, h, raw, first.NextPageToken)
}

// checkRefusedTokens proves that a token with a changed cursor and a token of
// another query get 400 with the code of a page token that is not valid.
func checkRefusedTokens(t *testing.T, h http.Handler, raw []byte, token string) {
	t.Helper()

	for name, query := range map[string]string{
		"changed cursor":        "?pageToken=" + url.QueryEscape(changeCursor(t, raw)),
		"cursor of other query": "?unit=unt-a&pageToken=" + url.QueryEscape(token),
	} {
		code, got := getList(t, h, query)
		if code != http.StatusBadRequest || got.ErrorCode != errcode.RequestPageTokenInvalid {
			t.Errorf("%s = %d %q, want 400 request.page-token-invalid", name, code, got.ErrorCode)
		}
	}
}

// decodeToken returns the JSON of a page token.
func decodeToken(t *testing.T, token string) []byte {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode the token: %v", err)
	}

	return raw
}

// rawCursor returns the bytes that the cursor member of a token holds.
func rawCursor(t *testing.T, tokenJSON []byte) []byte {
	t.Helper()

	var wire map[string]string
	if err := json.Unmarshal(tokenJSON, &wire); err != nil {
		t.Fatalf("decode the wire: %v", err)
	}

	cursor, err := base64.RawURLEncoding.DecodeString(wire["j"])
	if err != nil {
		t.Fatalf("decode the cursor: %v", err)
	}

	return cursor
}

// changeCursor returns a token whose cursor has two characters changed.
func changeCursor(t *testing.T, tokenJSON []byte) string {
	t.Helper()

	var wire map[string]string
	if err := json.Unmarshal(tokenJSON, &wire); err != nil {
		t.Fatal(err)
	}

	wire["j"] = wire["j"][:len(wire["j"])-2] + "AA"

	changed, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}

	return base64.RawURLEncoding.EncodeToString(changed)
}

// TestListWithoutASealerScansToTheEnd proves the other path: with no sealer
// the scan has no limit, so no token can hold the ID of a hidden job.
func TestListWithoutASealerScansToTheEnd(t *testing.T) {
	t.Parallel()

	h := pagedHandler(t, scanLimitRows(t), nil)

	code, got := getList(t, h, "")
	if code != http.StatusOK || len(got.Jobs) != 1 || got.NextPageToken != "" {
		t.Errorf("page = %d %v token %q, want the one readable job and no token", code, ids(got), got.NextPageToken)
	}
}

var errAuthorizerDown = errors.New("authorizer store down")

// failingAuthorizer fails the job:read check with an error that is no refusal.
type failingAuthorizer struct{ *recordingAuthorizer }

func (f failingAuthorizer) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionJobRead {
		return errAuthorizerDown
	}

	return f.recordingAuthorizer.Authorize(ctx, p, action, obj)
}

// TestListLeavesOutAJobWhenTheAuthorizerFails proves that any error of the
// check, not only a refusal, leaves the job out of the page.
func TestListLeavesOutAJobWhenTheAuthorizerFails(t *testing.T) {
	t.Parallel()

	jobs := newFakeJobs()
	jobs.listed = func(job.Filter) []job.Job {
		return []job.Job{listedJob(t, "acme", "job-aaaaaaaaaaaaaaaaaaaa", "unt-a")}
	}

	h := newWorkerHandler(t, failingAuthorizer{newAuthorizer(readsUnits("unt-a")...)}, handlerDeps{
		jobs: jobs, starter: nil, entries: nil, artifacts: nil, logs: nil, cursors: nil,
	}, nil)

	code, got := getList(t, h, "")
	if code != http.StatusOK || len(got.Jobs) != 0 {
		t.Errorf("page = %d %v, want an empty page", code, ids(got))
	}
}
