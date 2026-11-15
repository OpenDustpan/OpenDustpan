package github_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// listed is a commit of the list that FindCommit reads. A nil author, committer
// or verification is JSON null, or left out for the verification.
type listed struct {
	sha       string
	message   string
	author    any
	committer any
	// verification is left out when it is nil. json.RawMessage("null") makes a
	// JSON null.
	verification any
}

// json returns the commit as the list of GitHub shows it. The message gets the
// proof line of the job j-1 on the base rawBase after its last line, as a commit
// of the connection has.
func (l listed) json(at time.Time) map[string]any {
	message := strings.TrimRight(l.message, " \t\r\n") + "\n" + testProofLine(rawRepoID, "Dustpan-Job: j-1", rawBase)

	inner := map[string]any{
		"message":   message,
		"committer": map[string]any{"date": at.UTC().Format(time.RFC3339)},
	}

	if l.verification != nil {
		inner["verification"] = l.verification
	}

	return map[string]any{"sha": l.sha, "commit": inner, "author": l.author, "committer": l.committer}
}

func login(name string) map[string]any { return map[string]any{"login": name} }

// findTrusted answers FindCommit of the trailer with the list of commits, for
// a connection whose identity is rawIdentity.
func findTrusted(t *testing.T, list []listed, trailer string) (forge.Commit, error) {
	t.Helper()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := make([]map[string]any, 0, len(list))

	for _, l := range list {
		items = append(items, l.json(since.Add(time.Minute)))
	}

	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}

	fg := rawForge(t, "", func(w http.ResponseWriter, _ *http.Request) { answer(w, http.StatusOK, string(data)) })

	return fg.FindCommit(t.Context(), "acme/infra", "main", rawDelivery(trailer, rawBase), since)
}

// wantFound checks the answer of FindCommit: the commit wantID when wantErr is
// nil, and else no commit and an error that matches wantErr.
func wantFound(t *testing.T, got forge.Commit, err error, wantID string, wantErr error) {
	t.Helper()

	if !errors.Is(err, wantErr) || got.ID != wantID {
		t.Fatalf("FindCommit = %+v, %v; want %q and %v", got, err, wantID, wantErr)
	}
}

// identityRows are the rows of TestFindCommitOnlyAcceptsACommitOfTheConnection.
// Each row differs from the commit of the connection in one field.
func identityRows() []struct {
	name    string
	commit  listed
	wantErr error
} {
	verified := map[string]any{"verified": true}
	web := login("web-flow")
	own := login(rawIdentity)
	untrusted := forge.ErrUntrustedCommit

	row := func(name string, author, committer, verification any, wantErr error) struct {
		name    string
		commit  listed
		wantErr error
	} {
		return struct {
			name    string
			commit  listed
			wantErr error
		}{
			name: name,
			commit: listed{
				sha: shaN(1), message: "Fix\n\nDustpan-Job: j-1", author: author, committer: committer,
				verification: verification,
			},
			wantErr: wantErr,
		}
	}

	return []struct {
		name    string
		commit  listed
		wantErr error
	}{
		row("the commit of the connection", own, web, verified, nil),
		// The verification check. GitHub sets the logins from the emails of the
		// commit, so a push with the noreply emails passes the two checks
		// below and only this check stops it.
		row("a host that reports no verification", own, web, nil, untrusted),
		row("a verification that is null", own, web, json.RawMessage("null"), untrusted),

		// The author check.
		row("another author", login("mallory"), web, verified, untrusted),
		row("no account as author", nil, web, verified, untrusted),
		row("an author with no login", map[string]any{}, web, verified, untrusted),
		row("GitHub as author", web, web, verified, untrusted),
		row("the login of the connection in other letters", login("ACME"), web, verified, untrusted),
		row("the login of the connection with a suffix", login(rawIdentity+"[bot]"), web, verified, untrusted),
		row("an author login with a control character", login(rawIdentity+"\n"), web, verified, untrusted),

		// The committer check.
		row("another committer", own, login("mallory"), verified, untrusted),
		row("no account as committer", own, nil, verified, untrusted),
		row("the connection as committer", own, own, verified, untrusted),
		row("a committer with no login", own, map[string]any{}, verified, untrusted),
		row("a committer that only looks like GitHub", own, login("web-flow2"), verified, untrusted),
		row("a committer with the name of GitHub in capitals", own, login("WEB-FLOW"), verified, untrusted),

		// The verification check.
		row("a commit that is not verified", own, web, map[string]any{"verified": false}, untrusted),
		row("a verification with no verified field", own, web, map[string]any{"reason": "valid"}, untrusted),
		row("a verification with a null verified field", own, web, map[string]any{"verified": nil}, untrusted),
		row("a verified field that is a text", own, web, map[string]any{"verified": "true"}, forge.ErrUnavailable),
	}
}

// TestFindCommitOnlyAcceptsACommitOfTheConnection gives the commit with the
// trailer in the list. Each row differs from the commit of the connection in
// one field, so that each check of FindCommit stops its row alone.
func TestFindCommitOnlyAcceptsACommitOfTheConnection(t *testing.T) {
	t.Parallel()

	for _, tt := range identityRows() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := findTrusted(t, []listed{tt.commit}, "Dustpan-Job: j-1")

			want := ""
			if tt.wantErr == nil {
				want = shaN(1)
			}

			wantFound(t, got, err, want, tt.wantErr)

			// The error names the commit, and holds no text of the host.
			if errors.Is(err, forge.ErrUntrustedCommit) &&
				(!strings.Contains(err.Error(), shaN(1)) || strings.ContainsAny(err.Error(), "\n\r")) {
				t.Fatalf("error text %q: want the commit ID and no control character", err.Error())
			}
		})
	}
}

// TestFindCommitStopsAtTheNewestCommitWithTheTrailer checks the order: the
// newest commit with the trailer decides, and a commit with no trailer never
// raises the error.
func TestFindCommitStopsAtTheNewestCommitWithTheTrailer(t *testing.T) {
	t.Parallel()

	trailer := "Dustpan-Job: j-1"
	verified := map[string]any{"verified": true}

	own := func(n int) listed {
		return listed{
			sha: shaN(n), message: "Fix\n\n" + trailer, author: login(rawIdentity), committer: login("web-flow"),
			verification: verified,
		}
	}
	pushed := func(n int) listed {
		return listed{
			sha: shaN(n), message: "Pick\n\n" + trailer, author: login("mallory"), committer: login("mallory"),
			verification: nil,
		}
	}
	noTrailer := func(n int) listed {
		return listed{
			sha: shaN(n), message: "Pick\n\nOther: x", author: login("mallory"), committer: login("mallory"),
			verification: nil,
		}
	}

	tests := []struct {
		name    string
		list    []listed
		want    string
		wantErr error
	}{
		{"a pushed commit above the own commit", []listed{pushed(1), own(2)}, "", forge.ErrUntrustedCommit},
		{"the own commit above a pushed commit", []listed{own(1), pushed(2)}, shaN(1), nil},
		{"a pushed commit with no trailer above the own commit", []listed{noTrailer(1), own(2)}, shaN(2), nil},
		{"only a pushed commit", []listed{pushed(1)}, "", forge.ErrUntrustedCommit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := findTrusted(t, tt.list, trailer)
			wantFound(t, got, err, tt.want, tt.wantErr)
		})
	}
}

// TestFindCommitFailsClosedWhenTheIdentityIsUnknown gives an answer for the
// user of the token that cannot give the identity of the connection. No commit
// counts then, also a commit that looks right.
func TestFindCommitFailsClosedWhenTheIdentityIsUnknown(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	trailer := "Dustpan-Job: j-1"

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"a refused token", http.StatusUnauthorized, `{}`, forge.ErrAuth},
		{"a server error", http.StatusServiceUnavailable, `{}`, forge.ErrUnavailable},
		{"an answer that does not parse", http.StatusOK, `not json`, forge.ErrUnavailable},
		{"an answer with no login", http.StatusOK, `{"id":777}`, forge.ErrUnavailable},
		{"a login with a control character", http.StatusOK, `{"id":777,"login":"acme\n"}`, forge.ErrUnavailable},
		{"a login with a slash", http.StatusOK, `{"id":777,"login":"acme/x"}`, forge.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			own := listed{
				sha: shaN(1), message: "Fix\n\n" + trailer, author: login(rawIdentity), committer: login("web-flow"),
				verification: map[string]any{"verified": true},
			}

			data, err := json.Marshal([]map[string]any{own.json(since.Add(time.Minute))})
			if err != nil {
				t.Fatal(err)
			}

			fg := rawForgeAs(t, "", tt.status, tt.body, func(w http.ResponseWriter, _ *http.Request) {
				answer(w, http.StatusOK, string(data))
			})

			got, findErr := fg.FindCommit(
				t.Context(), "acme/infra", "main", rawDelivery(trailer, rawBase), since,
			)
			wantFound(t, got, findErr, "", tt.wantErr)
		})
	}
}

// treeOf returns the JSON of a tree with the entries, each "path mode sha".
func treeOf(entries ...[3]string) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, `{"path":`+strconv.Quote(e[0])+`,"mode":`+strconv.Quote(e[1])+
			`,"type":"blob","sha":`+strconv.Quote(e[2])+`}`)
	}

	return `{"tree":[` + strings.Join(parts, ",") + `],"truncated":false}`
}

// Object IDs and modes of the trees of TestCommitComparesTheTreeEntries.
const (
	idBlobOne = "1111111111111111111111111111111111111111"
	idBlobTwo = "2222222222222222222222222222222222222222"
	idDirBase = "3333333333333333333333333333333333333333"
	idDirHead = "4444444444444444444444444444444444444444"

	modeFile   = "100644"
	modeExec   = "100755"
	modeLink   = "120000"
	modeModule = "160000"
	modeDir    = "040000"
)

// answerOf is the answer for the root tree of the base or of the head.
type answerOf struct {
	code int // The status; zero means 200.
	body string
}

func okTree(body string) answerOf { return answerOf{code: http.StatusOK, body: body} }

// treeRow is a row of TestCommitComparesTheTreeEntries.
type treeRow struct {
	name string
	path string
	base answerOf
	head answerOf
	want error
	// proceeded is whether the commit goes on the new head.
	proceeded bool
}

// treeRows are the rows of TestCommitComparesTheTreeEntries.
func treeRows() []treeRow {
	none := treeOf()
	entry := func(mode, sha string) string { return treeOf([3]string{"a.txt", mode, sha}) }
	conflict := forge.ErrConflict

	row := func(name, path string, base, head answerOf, want error, proceeded bool) treeRow {
		return treeRow{name: name, path: path, base: base, head: head, want: want, proceeded: proceeded}
	}
	same := func(name, base, head string) treeRow {
		return row(name, "a.txt", okTree(base), okTree(head), conflict, false)
	}

	// nested is a conflict for the path "stacks/prod.yaml".
	nested := func(name, base, head string) treeRow {
		return row(name, "stacks/prod.yaml", okTree(base), okTree(head), conflict, false)
	}

	return []treeRow{
		row("no entry on both sides", "a.txt", okTree(none), okTree(none), nil, true),
		row(
			"the same regular file",
			"a.txt",
			okTree(entry(modeFile, idBlobOne)),
			okTree(entry(modeFile, idBlobOne)),
			nil,
			true,
		),
		row(
			"the same executable file",
			"a.txt",
			okTree(entry(modeExec, idBlobOne)),
			okTree(entry(modeExec, idBlobOne)),
			nil,
			true,
		),
		same("another blob", entry(modeFile, idBlobOne), entry(modeFile, idBlobTwo)),
		same("the same blob and another mode", entry(modeFile, idBlobOne), entry(modeExec, idBlobOne)),
		same("a new file at the head", none, entry(modeFile, idBlobOne)),
		same("a deleted file at the head", entry(modeFile, idBlobOne), none),

		same("a new symlink at the head", none, entry(modeLink, idBlobOne)),
		same("the same symlink on both sides", entry(modeLink, idBlobOne), entry(modeLink, idBlobOne)),
		same("two symlinks with other targets", entry(modeLink, idBlobOne), entry(modeLink, idBlobTwo)),
		same("a file at the base and a symlink at the head", entry(modeFile, idBlobOne), entry(modeLink, idBlobOne)),
		same("a symlink at the base and a file at the head", entry(modeLink, idBlobOne), entry(modeFile, idBlobOne)),

		same("a new submodule at the head", none, entry(modeModule, idBlobOne)),
		same("the same submodule on both sides", entry(modeModule, idBlobOne), entry(modeModule, idBlobOne)),
		same("two submodules with other commits", entry(modeModule, idBlobOne), entry(modeModule, idBlobTwo)),

		same("a new directory at the head", none, entry(modeDir, idDirHead)),
		same("the same directory on both sides", entry(modeDir, idDirBase), entry(modeDir, idDirBase)),

		same("a mode that the adapter does not know", entry("100664", idBlobOne), entry("100664", idBlobOne)),
		same("a mode that is not octal", entry("10064x", idBlobOne), entry("10064x", idBlobOne)),
		same("a mode that is too short", entry("644", idBlobOne), entry("644", idBlobOne)),

		nested("a parent name that is a symlink at the head", treeOf([3]string{"stacks", modeDir, idDirBase}),
			treeOf([3]string{"stacks", modeLink, idBlobOne})),
		nested("a parent name that is a file at the head", none, treeOf([3]string{"stacks", modeFile, idBlobOne})),
		nested("a parent name that is a submodule on both sides", treeOf([3]string{"stacks", modeModule, idBlobOne}),
			treeOf([3]string{"stacks", modeModule, idBlobOne})),

		same("a tree that GitHub cuts", none, `{"tree":[],"truncated":true}`),
		// Without the check, the last entry wins and equals the base.
		same(
			"a name twice",
			entry(modeFile, idBlobOne),
			treeOf([3]string{"a.txt", modeLink, idBlobTwo}, [3]string{"a.txt", modeFile, idBlobOne}),
		),
		same("a name with a slash", none, treeOf([3]string{"x/a.txt", modeFile, idBlobOne})),
		same("a name that is two dots", none, treeOf([3]string{"..", modeFile, idBlobOne})),
		same("an object ID that is not valid", none, entry(modeFile, "main")),
		same("an object ID in capitals", none, entry(modeFile, strings.ToUpper(idBlobOne[:30])+idBlobOne[:10])),
		same("a tree that does not parse", none, `not json`),
		row(
			"a base tree that GitHub does not have",
			"a.txt",
			answerOf{code: http.StatusNotFound, body: `{}`},
			okTree(none),
			conflict,
			false,
		),
		row("a server error", "a.txt", okTree(none), answerOf{code: http.StatusServiceUnavailable, body: `{}`},
			forge.ErrUnavailable, false),
		row("a refused credential", "a.txt", okTree(none), answerOf{code: http.StatusUnauthorized, body: `{}`},
			forge.ErrAuth, false),
	}
}

// movedBranch plays GitHub for a branch that moved from rawBase to rawCommit.
type movedBranch struct {
	mu        sync.Mutex
	mutations int
	base      answerOf
	head      answerOf
}

func (m *movedBranch) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case strings.Contains(r.URL.Path, "/git/ref/heads/"):
		answer(w, http.StatusOK, `{"object":{"sha":"`+rawCommit+`"}}`)
	case strings.Contains(r.URL.Path, "/compare/"):
		answer(w, http.StatusOK, aheadOf(rawBase))
	case strings.HasSuffix(r.URL.Path, "/git/trees/"+rawBase):
		answer(w, m.base.code, m.base.body)
	case strings.HasSuffix(r.URL.Path, "/git/trees/"+rawCommit):
		answer(w, m.head.code, m.head.body)
	case strings.Contains(r.URL.Path, "/git/trees/"):
		// A tree below the root: empty, so that the path is missing.
		answer(w, http.StatusOK, treeOf())
	case m.mutations == 0:
		m.mutations++

		answer(w, http.StatusOK, typedErrors("x", "STALE_DATA"))
	default:
		m.mutations++

		answer(w, http.StatusOK, goodAnswer())
	}
}

// TestCommitComparesTheTreeEntries gives a branch that moved. The base and the
// head have trees that the host could forge. The commit goes on only when the
// path has no entry on both sides, or the same entry of a regular file; any
// other answer is a conflict and makes no second mutation.
func TestCommitComparesTheTreeEntries(t *testing.T) {
	t.Parallel()

	for _, tt := range treeRows() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := &movedBranch{mu: sync.Mutex{}, mutations: 0, base: tt.base, head: tt.head}
			fg := rawForge(t, "", host.serve)

			req := oneCommit(rawBase)
			req.Changes[0].Path = tt.path

			if _, err := fg.Commit(t.Context(), "acme/infra", req); !errors.Is(err, tt.want) {
				t.Fatalf("Commit = %v, want %v", err, tt.want)
			}

			host.mu.Lock()
			defer host.mu.Unlock()

			wantMutations := 1
			if tt.proceeded {
				wantMutations = 2
			}

			if host.mutations != wantMutations {
				t.Fatalf(
					"%d mutations, want %d: the commit went on a head that it must not overwrite",
					host.mutations,
					wantMutations,
				)
			}
		})
	}
}

// TestFindCommitAcceptsTheLoginOfAnEnterpriseManagedUser checks that the
// identity of a token user whose login has "_" is the author of its own
// commit. Without "_" in the allow list, a takeover never ends.
func TestFindCommitAcceptsTheLoginOfAnEnterpriseManagedUser(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	trailer := "Dustpan-Job: j-1"
	own := listed{
		sha: shaN(1), message: "Fix\n\n" + trailer, author: login("jdoe_acme"), committer: login("web-flow"),
		verification: map[string]any{"verified": true},
	}

	data, err := json.Marshal([]map[string]any{own.json(since.Add(time.Minute))})
	if err != nil {
		t.Fatal(err)
	}

	fg := rawForgeAs(
		t,
		"",
		http.StatusOK,
		`{"id":777,"login":"jdoe_acme"}`,
		func(w http.ResponseWriter, _ *http.Request) {
			answer(w, http.StatusOK, string(data))
		},
	)

	got, findErr := fg.FindCommit(
		t.Context(),
		"acme/infra",
		"main",
		rawDelivery(trailer, rawBase),
		since,
	)
	wantFound(t, got, findErr, shaN(1), nil)
}
