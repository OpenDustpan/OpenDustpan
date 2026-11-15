package dustpan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// restoredDev holds the secrets of one dev person that a restore brought in.
type restoredDev struct {
	subject string
	token   string
	session session.Secret
}

// openKindStore opens a SQLite store at path and claims kind in it.
func openKindStore(t *testing.T, path string, kind installation.Kind) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if _, err := st.ClaimKind(t.Context(), kind); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	return st
}

// newTokenSecret returns an access token in the form of a secret.
func newTokenSecret(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("random token: %v", err)
	}

	return accesstoken.SecretPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// devPeopleRows returns the rows of two dev people, each with a valid session
// and a valid token, and the secrets that open them.
func devPeopleRows(t *testing.T) (map[string][]map[string]any, []restoredDev) {
	t.Helper()

	nowMs := time.Now().UnixMilli()
	rows := map[string][]map[string]any{}
	people := make([]restoredDev, 0, 2)

	for i, subject := range []string{"dev:admin", "DEV:Admin"} {
		id := fmt.Sprintf("prn-dev%d", i)

		secret, err := session.NewSecret()
		if err != nil {
			t.Fatalf("new session secret: %v", err)
		}

		person := restoredDev{subject: subject, token: newTokenSecret(t), session: secret}
		people = append(people, person)

		sess := storetest.DevSessionRow(fmt.Sprintf("ses-dev%d", i), id, "dev")
		sess["digest"] = session.DigestOf(secret).String()
		sess["created_at_ms"], sess["last_seen_at_ms"], sess["auth_time_ms"] = nowMs, nowMs, nowMs

		sum := sha256.Sum256([]byte(person.token))
		tok := storetest.DevTokenRow(fmt.Sprintf("tok-dev%d", i), id)
		tok["digest"] = hex.EncodeToString(sum[:])
		tok["created_at_ms"] = nowMs

		rows["principals"] = append(rows["principals"], storetest.DevPrincipalRow(id, subject))
		rows["sessions"] = append(rows["sessions"], sess)
		rows["access_tokens"] = append(rows["access_tokens"], tok)
	}

	return rows, people
}

// restoreIntoProductionDatabase stages and swaps a backup of a dev
// installation that holds dev people into a database, and returns the path of
// the database after its kind became production. The checks of the layers 1
// and 2 do not run: a dev installation takes dev data, and the swap of a
// production installation would refuse the data (layer 3a).
func restoreIntoProductionDatabase(t *testing.T, rows map[string][]map[string]any) string {
	t.Helper()

	ctx := t.Context()

	src := openKindStore(t, filepath.Join(t.TempDir(), "src.db"), installation.KindDev)

	t.Cleanup(func() {
		if closeErr := src.Close(); closeErr != nil {
			t.Errorf("close the source database: %v", closeErr)
		}
	})

	files, sum, descs := storetest.PortableFiles(t, src)

	for _, table := range []string{"principals", "sessions", "access_tokens"} {
		files, descs, sum = storetest.AddPortableRows(t, files, descs, sum, table, rows[table]...)
	}

	dstPath := filepath.Join(t.TempDir(), "dst.db")
	dst := openKindStore(t, dstPath, installation.KindDev)

	opts := backup.StageOptions{
		BackupKind: installation.KindDev, AllowDev: false,
		RestoreAt: time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := dst.StagePortable(ctx, storetest.PortableOpener(files), sum, descs, opts); err != nil {
		t.Fatalf("stage the backup: %v", err)
	}

	if err := dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply the backup: %v", err)
	}

	if err := dst.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}

	// A copy of the files under the wrong command: the database says
	// production now.
	raw, err := sql.Open("sqlite", "file:"+dstPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open the database file: %v", err)
	}

	defer func() {
		_ = raw.Close() //nolint:errcheck // Test-only handle.
	}()

	if _, err := raw.ExecContext(ctx, `UPDATE installation SET kind = 'production'`); err != nil {
		t.Fatalf("set the kind: %v", err)
	}

	return dstPath
}

// TestRestoredDevPeopleAreRefusedOnProduction restores a backup that holds
// dev:admin and DEV:Admin, each with a valid session and a valid token, into a
// database that then runs as a production installation. The check of the
// session cookie and the check of the token refuse both people, and accept
// them where the refusal is off, so the refusal is the only reason (layer 3b).
func TestRestoredDevPeopleAreRefusedOnProduction(t *testing.T) {
	t.Parallel()

	rows, people := devPeopleRows(t)
	path := restoreIntoProductionDatabase(t, rows)

	prod, err := sqlite.Open(t.Context(), path, sqlite.WithInstallationKind(installation.KindProduction))
	if err != nil {
		t.Fatalf("open the production database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := prod.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	kind, err := prod.ReadKind(t.Context())
	if err != nil || kind != installation.KindProduction {
		t.Fatalf("kind of the restored database = %q, %v, want production", kind, err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: prod, Principals: prod, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		TempAdmins: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	guarded := principal.RefuseDevSubjects(tokens)

	sessions, err := session.NewSessions(session.SessionsConfig{Store: prod, Providers: nil, Inactivity: nil, Now: nil})
	if err != nil {
		t.Fatalf("session service: %v", err)
	}

	open, err := sessionauth.New(sessions, nil)
	if err != nil {
		t.Fatalf("open session checker: %v", err)
	}

	refusing, err := sessionauth.New(sessions, nil, sessionauth.RefuseDevSubjects())
	if err != nil {
		t.Fatalf("refusing session checker: %v", err)
	}

	for _, person := range people {
		checkTokenRefused(t, person, tokens, guarded)
		checkSessionRefused(t, person, open, refusing)
	}
}

// checkTokenRefused proves that the token of person authenticates through the
// service alone, and not through the dev guard.
func checkTokenRefused(
	t *testing.T, person restoredDev, open, guarded identity.Authenticator,
) {
	t.Helper()

	got, err := open.AuthenticateToken(t.Context(), person.token)
	if err != nil || got.Subject != person.subject {
		t.Fatalf("token of %s without the guard = %+v, %v, want the person", person.subject, got, err)
	}

	if _, err := guarded.AuthenticateToken(t.Context(), person.token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("token of %s with the guard: err = %v, want ErrUnauthenticated", person.subject, err)
	}
}

// checkSessionRefused proves that the session of person authenticates through
// the checker alone, and not through the checker that refuses dev people.
func checkSessionRefused(t *testing.T, person restoredDev, open, refusing *sessionauth.Checker) {
	t.Helper()

	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/inventory/namespaces", http.NoBody)
		r.AddCookie(&http.Cookie{
			Name: sessionauth.Cookie, Value: person.session.Reveal(), Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		return r
	}

	got, _, err := open.Authenticate(request(t.Context()))
	if err != nil || got.Subject != person.subject {
		t.Fatalf("session of %s without the refusal = %+v, %v, want the person", person.subject, got, err)
	}

	if _, _, err := refusing.Authenticate(request(t.Context())); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("session of %s with the refusal: err = %v, want ErrUnauthenticated", person.subject, err)
	}
}
