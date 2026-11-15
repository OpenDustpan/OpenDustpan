package forgetest

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// method is one operation of the port that calls the forge, with the
// capability that covers it.
type method struct {
	name string
	need forge.Capability
	call func(ctx context.Context, f forge.Forge, repo string) error
}

// validBase is a full commit ID for a call that needs one.
const validBase = "0123456789abcdef0123456789abcdef01234567"

// methods lists each operation of the port with valid-looking arguments.
// ParseEvent is in the list: it makes no call to the forge.
func methods(secret []byte) []method {
	return []method{
		{"Head", forge.CapFiles, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.Head(ctx, repo, "main")

			return err
		}},
		{"ReadFile", forge.CapFiles, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.ReadFile(ctx, repo, validBase, "README.md", 1<<20)

			return err
		}},
		{"Compare", forge.CapCompare, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.Compare(ctx, repo, validBase, validBase, 1<<20)

			return err
		}},
		{"Commit", forge.CapCommit, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.Commit(ctx, repo, forge.CommitRequest{
				Branch: "dustpan/j-1", NewBranch: true, Base: validBase, Message: "Change\n\nDustpan-Job: j-1",
				Trailer: "Dustpan-Job: j-1",
				Author:  forge.Person{Name: "Ada", Email: "ada@example.com"},
				Changes: []forge.FileChange{{Path: "a.txt", Content: []byte("a"), Delete: false}},
			})

			return err
		}},
		{"FindCommit", forge.CapCommit, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.FindCommit(
				ctx,
				repo,
				"main",
				anyDelivery("Dustpan-Job: j-1", validBase),
				time.Unix(0, 0),
			)

			return err
		}},
		{"OpenReview", forge.CapReview, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.OpenReview(
				ctx,
				repo,
				forge.ReviewRequest{Branch: "dustpan/j-1", Target: "main", Title: "t", Body: "b"},
			)

			return err
		}},
		{"ReadReview", forge.CapReview, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.ReadReview(ctx, repo, "1")

			return err
		}},
		{"CloseReview", forge.CapReview, func(ctx context.Context, f forge.Forge, repo string) error {
			return f.CloseReview(ctx, repo, "1")
		}},
		{"DeleteBranch", forge.CapReview, func(ctx context.Context, f forge.Forge, repo string) error {
			return f.DeleteBranch(ctx, repo, "dustpan/j-1")
		}},
		{"SetStatus", forge.CapStatus, func(ctx context.Context, f forge.Forge, repo string) error {
			return f.SetStatus(ctx, repo, validBase, forge.Status{
				State: forge.StatusSuccess, Context: "opendustpan/plan", Description: "d", TargetURL: "",
			})
		}},
		{"UpsertComment", forge.CapComment, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.UpsertComment(ctx, repo, "1", forge.CommentRequest{Marker: "plan", Body: "text"})

			return err
		}},
		{"ReadComment", forge.CapComment, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.ReadComment(ctx, repo, "1")

			return err
		}},
		{"React", forge.CapComment, func(ctx context.Context, f forge.Forge, repo string) error {
			return f.React(ctx, repo, "1", forge.ReactionEyes)
		}},
		{"Tree", forge.CapTree, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.Tree(ctx, repo, validBase, "", 10)

			return err
		}},
		{"RepoToken", forge.CapGitAccess, func(ctx context.Context, f forge.Forge, repo string) error {
			_, err := f.RepoToken(ctx, repo)

			return err
		}},
		{"ParseEvent", forge.CapEvents, func(ctx context.Context, f forge.Forge, _ string) error {
			_, err := f.ParseEvent(ctx, forge.EventRequest{Header: nil, Body: []byte("{}")}, [][]byte{secret})

			return err
		}},
		{"RotateSelf", forge.CapSelfRotate, func(ctx context.Context, f forge.Forge, _ string) error {
			_, err := f.RotateSelf(ctx, time.Time{})

			return err
		}},
	}
}

func caseCheckAccount(t *testing.T, h Harness) {
	t.Helper()

	got, err := h.Forge.Check(t.Context())
	if err != nil {
		t.Fatalf("Check = %v", err)
	}

	if got.ID != h.Account.ID || got.Login != h.Account.Login || got.ID == "" || got.Login == "" {
		t.Fatalf("Check account = %q %q, want %q %q", got.ID, got.Login, h.Account.ID, h.Account.Login)
	}
}

func caseCheckExpiry(t *testing.T, h Harness) {
	t.Helper()

	if h.Connection.Auth != forge.AuthToken {
		t.Skip("only a token has an expiry")
	}

	want := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	if err := h.Fixture.SetTokenExpiry(t.Context(), want); err != nil {
		t.Fatalf("set the expiry: %v", err)
	}

	got, err := h.Forge.Check(t.Context())
	if err != nil {
		t.Fatalf("Check = %v", err)
	}

	if !got.ExpiresAt.Equal(want) || got.NoExpiry {
		t.Fatalf("Check expiry = %v, no expiry %v; want %v and no flag", got.ExpiresAt, got.NoExpiry, want)
	}
}

func caseCheckNoExpiry(t *testing.T, h Harness) {
	t.Helper()

	if h.Connection.Auth != forge.AuthToken {
		t.Skip("only a token has an expiry")
	}

	if err := h.Fixture.SetTokenExpiry(t.Context(), time.Time{}); err != nil {
		t.Fatalf("remove the expiry: %v", err)
	}

	got, err := h.Forge.Check(t.Context())
	if err != nil {
		t.Fatalf("Check = %v", err)
	}

	if !got.NoExpiry || !got.ExpiresAt.IsZero() {
		t.Fatalf("Check expiry = %v, no expiry %v; want the flag and no time", got.ExpiresAt, got.NoExpiry)
	}
}

func caseOpenRefuses(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()

	before, err := h.Fixture.Calls(ctx)
	if err != nil {
		t.Fatalf("calls: %v", err)
	}

	noParams := h.Connection
	noParams.Params = maps.Clone(h.Connection.Params)
	clear(noParams.Params)
	noParams.BaseURL = ""

	otherKind := h.Connection
	otherKind.Kind = forge.KindGitLab

	otherAuth := h.Connection
	otherAuth.Auth = "unknown"

	badSecret := h.Credential
	badSecret.Secret = []byte("not a usable secret\n")

	if h.Connection.Auth == forge.AuthToken {
		badSecret.Secret = nil
	}

	type input struct {
		name string
		c    forge.Connection
		cred forge.Credential
	}

	inputs := []*input{
		{"another kind", otherKind, h.Credential},
		{"a missing parameter", noParams, h.Credential},
		{"an unknown auth kind", otherAuth, h.Credential},
		{"a bad secret", h.Connection, badSecret},
	}

	for _, in := range inputs {
		f, openErr := h.Opener.Open(ctx, in.c, in.cred)
		if !errors.Is(openErr, forge.ErrInvalid) || f != nil {
			t.Errorf("Open with %s = %v, %v; want ErrInvalid and no forge", in.name, f, openErr)
		}
	}

	after, err := h.Fixture.Calls(ctx)
	if err != nil {
		t.Fatalf("calls: %v", err)
	}

	if after != before {
		t.Fatalf("Open made %d calls to the forge, want none", after-before)
	}
}

func caseRefusedIsAuth(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()

	if err := h.Fixture.Refuse(ctx); err != nil {
		t.Fatalf("refuse: %v", err)
	}

	if _, err := h.Forge.Check(ctx); !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("Check = %v, want ErrAuth", err)
	}

	listed := h.Forge.Capabilities()

	for _, m := range methods(h.EventSecret) {
		// A method of events makes no call, so a refused credential does
		// not change it.
		if m.need == forge.CapEvents || !listed.Has(m.need) {
			continue
		}

		if err := m.call(ctx, h.Forge, h.Repo); !errors.Is(err, forge.ErrAuth) {
			t.Errorf("%s = %v, want ErrAuth", m.name, err)
		}
	}
}

func caseUnsupported(t *testing.T, h Harness) {
	t.Helper()

	listed := h.Forge.Capabilities()

	for _, m := range methods(h.EventSecret) {
		if listed.Has(m.need) {
			continue
		}

		if err := m.call(t.Context(), h.Forge, h.Repo); !errors.Is(err, forge.ErrUnsupported) {
			t.Errorf("%s = %v, want ErrUnsupported", m.name, err)
		}
	}
}

func caseNoSecretInErrors(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	canaries := parts(h.Credential.Secret, h.EventSecret)

	var errs []error

	// An open with a bad secret that holds the canary.
	bad := h.Credential
	bad.Secret = append([]byte("-----BEGIN PRIVATE KEY-----\n"), h.Credential.Secret...)

	if _, err := h.Opener.Open(ctx, h.Connection, bad); err != nil {
		errs = append(errs, err)
	}

	restore, err := h.Fixture.Garble(ctx)
	if err != nil {
		t.Fatalf("garble: %v", err)
	}

	_, err = h.Forge.Check(ctx)
	errs = append(errs, err)

	restore()

	restore, err = h.Fixture.Unavailable(ctx, 3*time.Second)
	if err != nil {
		t.Fatalf("unavailable: %v", err)
	}

	_, err = h.Forge.Check(ctx)
	errs = append(errs, err)

	restore()

	if refuseErr := h.Fixture.Refuse(ctx); refuseErr != nil {
		t.Fatalf("refuse: %v", refuseErr)
	}

	_, err = h.Forge.Check(ctx)
	errs = append(errs, err)

	for _, m := range methods(h.EventSecret) {
		errs = append(errs, m.call(ctx, h.Forge, h.Repo))
	}

	for _, e := range errs {
		if e == nil {
			continue
		}

		for _, c := range canaries {
			if strings.Contains(e.Error(), c) {
				t.Errorf("an error holds a part of a secret: %v", e)
			}
		}
	}

	if !errors.Is(errs[1], forge.ErrUnavailable) {
		t.Errorf("Check of a garbled answer = %v, want ErrUnavailable", errs[1])
	}
}

// parts returns pieces of each secret that no other text holds: each line
// of 16 bytes or more that is not a PEM marker, and for a secret with no such
// line, its two halves.
func parts(secrets ...[]byte) []string {
	var out []string

	for _, s := range secrets {
		before := len(out)

		for line := range bytes.SplitSeq(s, []byte("\n")) {
			if len(line) >= 16 && !bytes.HasPrefix(line, []byte("-----")) {
				out = append(out, string(line))
			}
		}

		if len(out) == before && len(s) >= 16 {
			out = append(out, string(s[:len(s)/2]), string(s[len(s)/2:]))
		}
	}

	return out
}

func caseUnavailable(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()

	restore, err := h.Fixture.Unavailable(ctx, 0)
	if err != nil {
		t.Fatalf("unavailable: %v", err)
	}

	_, err = h.Forge.Check(ctx)
	if !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("Check = %v, want ErrUnavailable", err)
	}

	restore()

	if _, err := h.Forge.Check(ctx); err != nil {
		t.Fatalf("Check after the restore = %v", err)
	}
}

func caseRateLimit(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	wait := 7 * time.Second

	restore, err := h.Fixture.Unavailable(ctx, wait)
	if err != nil {
		t.Fatalf("unavailable: %v", err)
	}

	defer restore()

	_, err = h.Forge.Check(ctx)
	if !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("Check = %v, want ErrUnavailable", err)
	}

	if got, ok := forge.RetryAfter(err); !ok || got != wait {
		t.Fatalf("RetryAfter = %v, %v; want %v", got, ok, wait)
	}
}

func caseNoPushNoWrite(t *testing.T, h Harness) {
	t.Helper()

	c := h.Connection
	c.Push = false

	f, err := h.Opener.Open(t.Context(), c, h.Credential)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}

	caps := f.Capabilities()
	if caps.Has(forge.CapCommit) || caps.Has(forge.CapReview) || caps.Has(forge.CapStatus) ||
		caps.Has(forge.CapComment) {
		t.Fatalf("a connection with no push access lists %v", caps)
	}
}

func caseRepoTokenImpliesGitAccess(t *testing.T, h Harness) {
	t.Helper()

	if !h.Forge.Capabilities().Has(forge.CapGitAccess) {
		t.Fatal("the forge lists repo-token and not git-access")
	}
}
