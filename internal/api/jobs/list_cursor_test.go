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

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
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
	jobs.listed = func(f job.Filter) job.Page {
		start := afterIndex(rows, f.After)
		end := min(start+f.Limit, len(rows))

		out := job.Page{Jobs: append([]job.Job(nil), rows[start:end]...), Damaged: nil, After: ""}
		if end < len(rows) {
			out.After = rows[end-1].ID
		}

		return out
	}

	return newWorkerHandler(t, newAuthorizer(readsUnits("unt-a")...), handlerDeps{
		jobs: jobs, units: nil, starter: nil, entries: nil, artifacts: nil, logs: nil, cursors: cursors,
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
		"changed cursor":         "?pageToken=" + url.QueryEscape(changeCursor(t, raw)),
		"non-canonical encoding": "?pageToken=" + url.QueryEscape(respellCursor(t, raw)),
		"cursor of other query":  "?unit=unt-a&pageToken=" + url.QueryEscape(token),
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

// changeCursor returns a token whose sealed cursor has one bit changed. It
// changes the decoded bytes, not the text: a change of the last characters of
// the text can leave the bytes the same, because the decoder ignores the
// unused bits of the last character.
func changeCursor(t *testing.T, tokenJSON []byte) string {
	t.Helper()

	sealed := rawCursor(t, tokenJSON)
	if len(sealed) == 0 {
		t.Fatal("the token has an empty cursor")
	}

	sealed[len(sealed)/2] ^= 0x01

	return withCursor(t, tokenJSON, base64.RawURLEncoding.EncodeToString(sealed))
}

// respellCursor returns a token whose cursor holds the same sealed bytes in a
// text that is not the canonical encoding: the unused bits of the last
// character are set.
func respellCursor(t *testing.T, tokenJSON []byte) string {
	t.Helper()

	sealed := rawCursor(t, tokenJSON)
	if len(sealed)%3 == 0 {
		// Defense against a vacuous case: with no unused bits, every text is
		// canonical. Add a byte so that the last character has unused bits.
		t.Fatalf("the sealed cursor has %d bytes, a multiple of 3, so it has no unused bits", len(sealed))
	}

	text := []byte(base64.RawURLEncoding.EncodeToString(sealed))
	last := strings.IndexByte(alphabet, text[len(text)-1])
	text[len(text)-1] = alphabet[last|unusedBits(len(sealed))]

	if string(text) == base64.RawURLEncoding.EncodeToString(sealed) {
		t.Fatal("the respelled cursor equals the canonical text")
	}

	return withCursor(t, tokenJSON, string(text))
}

// alphabet is the alphabet of base64.RawURLEncoding, in value order.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// unusedBits returns the mask of the bits of the last character of the
// unpadded encoding of n bytes that hold no data.
func unusedBits(n int) int {
	if n%3 == 1 {
		return 0x0f
	}

	return 0x03
}

// withCursor returns the page token of tokenJSON with the cursor text cursor.
func withCursor(t *testing.T, tokenJSON []byte, cursor string) string {
	t.Helper()

	var wire map[string]string
	if err := json.Unmarshal(tokenJSON, &wire); err != nil {
		t.Fatal(err)
	}

	wire["j"] = cursor

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

// TestListScanLimitCountsDamagedRows proves that a damaged row counts against
// the scan limit, whether the caller may list its unit or not: a list whose
// pages hold damaged rows only ends at the limit, answers a token, and reads
// the store no more than a scan of full pages at the limit takes.
func TestListScanLimitCountsDamagedRows(t *testing.T) {
	t.Parallel()

	// A scan of full pages at the limit reads the store this many times, with
	// one more read for the page that the limit cuts. A store that ignores
	// the limit would loop forever, so the fake fails the test after the
	// bound plus a margin.
	bound := jobsapi.MaxScanned/jobsapi.ScanBatch + 1
	margin := 5

	calls := 0

	jobs := newFakeJobs()
	jobs.listed = func(job.Filter) job.Page {
		calls++
		if calls > bound+margin {
			t.Fatalf("the list read the store %d times, want at most %d: a damaged row does not count "+
				"against the scan limit", calls, bound)
		}

		damaged := make([]job.DamagedRow, 0, jobsapi.ScanBatch)
		for i := range jobsapi.ScanBatch {
			damaged = append(damaged, job.DamagedRow{
				ID:     job.ID(fmt.Sprintf("job-d%019d", (calls-1)*jobsapi.ScanBatch+i)),
				UnitID: "unt-a",
				Reason: "the body is not JSON",
			})
		}

		return job.Page{Jobs: nil, Damaged: damaged, After: damaged[len(damaged)-1].ID}
	}

	h := newWorkerHandler(t, newAuthorizer(readsNamespace()...), handlerDeps{
		jobs: jobs, units: nil, starter: nil, entries: nil, artifacts: nil, logs: nil, cursors: testWrapper(t),
	}, nil)

	code, got := getList(t, h, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if len(got.Jobs) != 0 {
		t.Errorf("jobs = %v, want none", ids(got))
	}

	if got.NextPageToken == "" {
		t.Error("nextPageToken is empty, want the token that continues after the last row read")
	}

	if calls > bound {
		t.Errorf("the list read the store %d times, want at most %d", calls, bound)
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
	jobs.listed = func(job.Filter) job.Page {
		return job.Page{
			Jobs: []job.Job{listedJob(t, "acme", "job-aaaaaaaaaaaaaaaaaaaa", "unt-a")}, Damaged: nil,
			After: "",
		}
	}

	h := newWorkerHandler(t, failingAuthorizer{newAuthorizer(readsUnits("unt-a")...)}, handlerDeps{
		jobs: jobs, units: nil, starter: nil, entries: nil, artifacts: nil, logs: nil, cursors: nil,
	}, nil)

	code, got := getList(t, h, "")
	if code != http.StatusOK || len(got.Jobs) != 0 {
		t.Errorf("page = %d %v, want an empty page", code, ids(got))
	}
}
