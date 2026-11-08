package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// subjectAuthenticator resolves each token to a person with one subject.
type subjectAuthenticator struct{ subject string }

func (a subjectAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: a.subject,
		DisplayName: "", Admin: false, TokenID: "tok", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialSession, JobID: "",
	}, nil
}

// devRefusalWorld is a settings value over a SQLite store, with a limiter on
// a clock that the test moves.
type devRefusalWorld struct {
	cfg *settings
	st  *sqlite.Store
	now time.Time
}

func newDevRefusalWorld(t *testing.T, kind installation.Kind) *devRefusalWorld {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	w := &devRefusalWorld{cfg: defaultSettings(), st: st, now: time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)}
	w.cfg.installationKind = kind
	w.cfg.namespaceStore = st
	w.cfg.logger = slog.New(slog.DiscardHandler)
	w.cfg.denialLimiter = denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: func() time.Time { return w.now },
	})

	w.bind(t)

	return w
}

// bind builds the recorder from the current settings of the world. A test that
// replaces the limiter calls it again.
func (w *devRefusalWorld) bind(t *testing.T) {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: w.st, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if err := w.cfg.devRefusal.bind(w.cfg, namespaces, nil); err != nil {
		t.Fatalf("bind: %v", err)
	}
}

// refuse authenticates a token of a dev person and checks the refusal.
func (w *devRefusalWorld) refuse(t *testing.T, subject string) {
	t.Helper()

	if err := w.signIn(t, subject); err == nil {
		t.Fatalf("sign-in of %q succeeded, want a refusal", subject)
	}
}

// signIn authenticates a token of a person with subject through the guard.
func (w *devRefusalWorld) signIn(t *testing.T, subject string) error {
	t.Helper()

	w.cfg.authenticator = subjectAuthenticator{subject: subject}

	ports, err := newAuthPorts(w.cfg, nil)
	if err != nil {
		t.Fatalf("auth ports: %v", err)
	}

	_, err = ports.authenticator.AuthenticateToken(t.Context(), "token")

	return err
}

// entries returns the denied sign-in entries of subject in the installation scope.
func (w *devRefusalWorld) entries(t *testing.T, subject string) []audit.Entry {
	t.Helper()

	list, err := w.st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list installation entries: %v", err)
	}

	var out []audit.Entry

	for i := range list.Entries {
		e := list.Entries[i]
		if e.Subject == subject && e.Action == session.ActionSignIn {
			out = append(out, e)
		}
	}

	return out
}

// flush writes the counts that the limiter holds, as the shutdown does.
func (w *devRefusalWorld) flush(t *testing.T) {
	t.Helper()

	w.cfg.devRefusal.recorder().Flush(t.Context())
}

// TestDevRefusalFirstWritesEntry proves that the first refusal of a dev person writes an entry that holds no token.
func TestDevRefusalFirstWritesEntry(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)
	if err := w.signIn(t, "dev:admin"); err == nil {
		t.Fatal("sign-in of dev:admin succeeded, want a refusal")
	}

	got := w.entries(t, "dev:admin")
	if len(got) != 1 {
		t.Fatalf("entries = %+v, want 1", got)
	}

	e := got[0]
	if e.Outcome != audit.OutcomeDenied || e.Object != "@server" || e.Reason != devRefusalReason {
		t.Fatalf("entry = outcome %q, object %q, reason %q", e.Outcome, e.Object, e.Reason)
	}

	if e.TokenID != "" || strings.Contains(e.Reason, "token") {
		t.Fatalf("entry holds token data: %+v", e)
	}
}

// TestDevRefusalCanceledContextWritesEntry proves that the hook writes the entry also when the context of the request
// is already canceled, as it is when the client closes the connection.
func TestDevRefusalCanceledContextWritesEntry(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)
	w.cfg.authenticator = subjectAuthenticator{subject: "dev:admin"}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	devRefusalAudit(w.cfg)(ctx, "dev:admin")

	got := w.entries(t, "dev:admin")
	if len(got) != 1 {
		t.Fatalf("entries after a canceled context = %+v, want 1", got)
	}

	if got[0].Outcome != audit.OutcomeDenied || got[0].Reason != devRefusalReason {
		t.Fatalf("entry = outcome %q, reason %q", got[0].Outcome, got[0].Reason)
	}
}

// TestDevRefusalSecondIsCounted proves that the second refusal in the hour writes nothing, and the count goes out after
// the hour.
func TestDevRefusalSecondIsCounted(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)

	for range 3 {
		w.refuse(t, "dev:admin")
		// More than the default window of the limiter, less than an hour.
		w.now = w.now.Add(10 * time.Minute)
	}

	if got := w.entries(t, "dev:admin"); len(got) != 1 {
		t.Fatalf("entries within the hour = %d, want 1", len(got))
	}

	// The window ends: the next refusal carries the count.
	w.now = w.now.Add(time.Hour)
	w.refuse(t, "dev:admin")

	got := w.entries(t, "dev:admin")
	if len(got) != 2 || !hasCount(got, "2 further refusals since the last entry") {
		t.Fatalf("entries after the hour = %+v, want the second with a count of 2", got)
	}
}

// TestDevRefusalQuietKeyFlushes proves that the count of a quiet person goes out after the window, with the next
// refusal of anyone.
func TestDevRefusalQuietKeyFlushes(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)
	w.refuse(t, "dev:admin")
	w.refuse(t, "dev:admin")

	w.now = w.now.Add(90 * time.Minute)
	// A refusal of another person triggers the sweep of the limiter.
	w.refuse(t, "dev:other")

	if !hasCount(w.entries(t, "dev:admin"), "1 further refusals in the last window") {
		t.Fatalf("entries = %+v, want a flush entry with a count of 1", w.entries(t, "dev:admin"))
	}
}

// TestDevRefusalShutdownFlush proves that the shutdown flush writes the count that the limiter holds.
func TestDevRefusalShutdownFlush(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)
	for range 4 {
		w.refuse(t, "dev:admin")
	}

	w.flush(t)

	got := w.entries(t, "dev:admin")
	if !hasCount(got, "3 further refusals in the last window") {
		t.Fatalf("entries = %+v, want a flush entry with a count of 3", got)
	}

	// The flush entry names the key kind of the limiter.
	if !slices.ContainsFunc(got, func(e audit.Entry) bool { return e.Object == "signin/server" }) {
		t.Fatalf("entries = %+v, want a flush entry with the object signin/server", got)
	}
}

// TestDevRefusalPeopleSeparate proves that two dev people have their own entries and counts.
func TestDevRefusalPeopleSeparate(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)
	for range 3 {
		w.refuse(t, "dev:admin")
	}

	w.refuse(t, "DEV:Other")

	w.flush(t)

	if got := w.entries(t, "DEV:Other"); len(got) != 1 {
		t.Fatalf("entries of DEV:Other = %+v, want 1 and no count", got)
	}

	if !hasCount(w.entries(t, "dev:admin"), "2 further refusals in the last window") {
		t.Fatalf("entries of dev:admin = %+v, want a count of 2", w.entries(t, "dev:admin"))
	}
}

// TestDevRefusalOtherPersonWritesNothing proves that a person that is not a dev person writes no entry.
func TestDevRefusalOtherPersonWritesNothing(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)
	if err := w.signIn(t, "bo@example.com"); err != nil {
		t.Fatalf("sign-in of bo@example.com: %v", err)
	}

	w.flush(t)

	if got := w.entries(t, "bo@example.com"); len(got) != 0 {
		t.Fatalf("entries = %+v, want none", got)
	}
}

// TestDevRefusalDevInstallationWritesNothing proves that a dev installation accepts a dev person and writes no entry.
func TestDevRefusalDevInstallationWritesNothing(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindDev)
	if err := w.signIn(t, "dev:admin"); err != nil {
		t.Fatalf("sign-in of dev:admin on a dev installation: %v", err)
	}

	if got := w.entries(t, "dev:admin"); len(got) != 0 {
		t.Fatalf("entries = %+v, want none", got)
	}
}

// hasCount reports whether an entry has a reason with text.
func hasCount(entries []audit.Entry, text string) bool {
	for i := range entries {
		if strings.Contains(entries[i].Reason, text) {
			return true
		}
	}

	return false
}

// TestDevRefusalHostileSubject proves that the entry holds a cleaned subject:
// no CR, LF or NUL, and at most the bound of the backup report.
func TestDevRefusalHostileSubject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		subject string
	}{
		{name: "CR", subject: "dev:a\rb"},
		{name: "LF", subject: "dev:a\nb"},
		{name: "NUL", subject: "dev:a\x00b"},
		{name: "long", subject: "dev:" + strings.Repeat("x", 10000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newDevRefusalWorld(t, installation.KindProduction)
			w.refuse(t, tc.subject)

			list, err := w.st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 100})
			if err != nil {
				t.Fatalf("list installation entries: %v", err)
			}

			if len(list.Entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(list.Entries))
			}

			got := list.Entries[0].Subject
			if strings.ContainsAny(got, "\r\n\x00") || len(got) > backup.MaxReportedSubjectBytes ||
				!strings.HasPrefix(got, "dev:") {
				t.Fatalf("subject = %q (%d bytes), want a cleaned dev subject within the bound", got, len(got))
			}
		})
	}
}

// TestDevRefusalFloodBounds proves the flood limits: 1000 refusals of one
// person in one hour write one entry with the count after the hour, and
// refusals of many distinct people keep the limiter at its size cap.
func TestDevRefusalFloodBounds(t *testing.T) {
	t.Parallel()

	t.Run("one person", func(t *testing.T) {
		t.Parallel()

		w := newDevRefusalWorld(t, installation.KindProduction)

		for range 1000 {
			w.refuse(t, "dev:admin")
		}

		if got := w.entries(t, "dev:admin"); len(got) != 1 {
			t.Fatalf("entries within the hour = %d, want 1", len(got))
		}

		w.now = w.now.Add(time.Hour + time.Minute)
		w.refuse(t, "dev:admin")

		got := w.entries(t, "dev:admin")
		if len(got) != 2 || !hasCount(got, "999 further refusals since the last entry") {
			t.Fatalf("entries after the hour = %+v, want a second with a count of 999", got)
		}
	})

	t.Run("many people", func(t *testing.T) {
		t.Parallel()

		const maxKeys = 8

		w := newDevRefusalWorld(t, installation.KindProduction)
		w.cfg.denialLimiter = denylimit.New(denylimit.Config{
			Window:              time.Minute,
			MaxKeys:             maxKeys,
			MaxFlush:            0,
			MaxKeysPerPrincipal: 0,
			Now:                 func() time.Time { return w.now },
		})
		w.bind(t)

		for i := range 200 {
			w.refuse(t, fmt.Sprintf("dev:p%d", i))
		}

		if n := w.cfg.denialLimiter.Len(); n > maxKeys {
			t.Fatalf("limiter holds %d keys, want at most %d", n, maxKeys)
		}

		list, err := w.st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 500})
		if err != nil {
			t.Fatalf("list installation entries: %v", err)
		}

		if len(list.Entries) > maxKeys+1 {
			t.Fatalf("entries = %d, want at most %d", len(list.Entries), maxKeys+1)
		}
	})
}

// TestDevRefusalFlushesNamespaceKey proves that the recorder of the dev
// refusals writes the held count of a key in a namespace into that namespace:
// the shared limiter holds the keys of every adapter.
func TestDevRefusalFlushesNamespaceKey(t *testing.T) {
	t.Parallel()

	w := newDevRefusalWorld(t, installation.KindProduction)

	err := w.st.CreateNamespace(t.Context(), namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: w.now, Name: "team", PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: w.st, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	failures := 0

	err = w.cfg.devRefusal.bind(w.cfg, namespaces, func() { failures++ })
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Another adapter holds the count of a key in the namespace.
	otherRec, err := refusal.New(nil, w.st, namespaces, w.cfg.logger, nil)
	if err != nil {
		t.Fatalf("refusal.New: %v", err)
	}

	other := otherRec.WithLimiter(w.cfg.denialLimiter)
	bo := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "bo@example.com",
		DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: "", JobID: "",
	}

	for range 3 {
		other.Record(t.Context(), nil, bo, refusal.Denial{
			Action: identity.ActionUnitWrite, Object: identity.NamespaceObject("team"), Reason: "denied",
			ReasonWithin: nil, Window: 0,
		})
	}

	w.now = w.now.Add(2 * time.Hour)
	// A dev refusal triggers the sweep, and its recorder writes the flush entry.
	w.refuse(t, "dev:admin")

	list, err := w.st.List(t.Context(), namespace.Name("team"), audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list namespace entries: %v", err)
	}

	if !hasCount(list.Entries, "2 further refusals in the last window") {
		t.Fatalf("namespace entries = %+v, want the flush entry with a count of 2", list.Entries)
	}

	if failures != 0 {
		t.Fatalf("audit failures = %d, want 0", failures)
	}
}
