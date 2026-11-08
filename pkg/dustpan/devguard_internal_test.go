package dustpan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// devPersonAuthenticator resolves each token to the person dev:admin.
type devPersonAuthenticator struct{}

func (devPersonAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: principal.DevAdminSubject,
		DisplayName: "Dev administrator", Admin: false, TokenID: "tok", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialSession, JobID: "",
	}, nil
}

// TestWireDevGuard proves the authentication layer of the server: a server
// that is not of the kind dev refuses the token of a dev person, and a server
// of the kind dev accepts it.
func TestWireDevGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind    installation.Kind
		wantErr error
	}{
		{kind: installation.KindProduction, wantErr: identity.ErrUnauthenticated},
		{kind: "", wantErr: identity.ErrUnauthenticated},
		{kind: installation.KindDev, wantErr: nil},
	}

	for _, tt := range tests {
		t.Run("kind "+tt.kind.String(), func(t *testing.T) {
			t.Parallel()

			cfg := defaultSettings()
			cfg.installationKind = tt.kind
			cfg.authenticator = devPersonAuthenticator{}

			ports, err := newAuthPorts(cfg, nil)
			if err != nil {
				t.Fatalf("auth ports: %v", err)
			}

			if _, err := ports.authenticator.AuthenticateToken(t.Context(), "token"); !errors.Is(err, tt.wantErr) {
				t.Fatalf("AuthenticateToken = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// warnServer returns a server over a store that holds the given subjects as
// people, and the buffer of its JSON log.
func warnServer(t *testing.T, kind installation.Kind, subjects ...string) (*Server, *bytes.Buffer) {
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

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: nil,
		Tokens:     nil,
		GroupReach: nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	for _, subject := range subjects {
		switch {
		case subject == principal.DevAdminSubject:
			_, _, err = svc.SignInDevAdministrator(t.Context(), adminEntry(), "Dev administrator")
		case principal.IsDevSubject(subject):
			// The service refuses every case of the dev prefix, as a sign-in
			// does. A row of another version or of a restore can still hold
			// one, so the case writes it through the store.
			entry := adminEntry()
			entry.Namespace = audit.ServerScope
			entry.At = time.Now()
			entry.Action = string(identity.ActionServerManagePrincipals)
			entry.Object = "principal:prn-" + subject
			entry.Kind = audit.KindChange
			entry.Outcome = audit.OutcomeSuccess

			err = st.CreatePrincipal(t.Context(), principal.Principal{
				CreatedAt: entry.At, UpdatedAt: entry.At, DisabledAt: time.Time{}, ID: "prn-" + subject,
				Subject: subject, DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal,
				Namespace: "", GroupCount: 0, BreakGlass: false,
			}, entry)
		default:
			_, err = svc.CreatePrincipal(t.Context(), adminEntry(), principal.Input{
				Subject: subject, DisplayName: "", Kind: principal.KindPerson,
			})
		}

		if err != nil {
			t.Fatalf("make %q: %v", subject, err)
		}
	}

	var out bytes.Buffer

	srv := new(Server)
	srv.logger = slog.New(slog.NewJSONHandler(&out, nil))
	srv.kind = kindState{store: nil, principals: st, kind: kind}

	return srv, &out
}

// warnCase is one case of TestWarnDevPrincipals.
type warnCase struct {
	name     string
	kind     installation.Kind
	subjects []string
	// want are the subjects that the warning names, or nil for no warning.
	want []string
}

// TestWarnDevPrincipals proves that the start of a server that is not of the
// kind dev logs one warning that names the dev people of the database, logs
// nothing on a server of the kind dev or for a database with no such person,
// and never returns an error.
func TestWarnDevPrincipals(t *testing.T) {
	t.Parallel()

	dev := []string{principal.DevAdminSubject}
	tests := []warnCase{
		{name: "production", kind: installation.KindProduction, subjects: []string{"alice", dev[0]}, want: dev},
		{name: "no kind", kind: "", subjects: dev, want: dev},
		{name: "dev kind", kind: installation.KindDev, subjects: dev, want: nil},
		{name: "no dev person", kind: installation.KindProduction, subjects: []string{"alice", "developer"}, want: nil},
		{
			name: "dev people in other cases", kind: installation.KindProduction,
			subjects: []string{"alice", "Dev:x", "DEV:admin"}, want: []string{"DEV:admin", "Dev:x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, out := warnServer(t, tt.kind, tt.subjects...)

			srv.warnDevPrincipals(t.Context())

			checkWarning(t, out.String(), tt.want)
		})
	}
}

// checkWarning proves that log holds one WARN line that names want, or no
// line when want is nil.
func checkWarning(t *testing.T, log string, want []string) {
	t.Helper()

	if want == nil {
		if log != "" {
			t.Fatalf("log = %q, want nothing", log)
		}

		return
	}

	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 1 {
		t.Fatalf("log has %d lines, want 1: %s", len(lines), log)
	}

	var record struct {
		Level    string   `json:"level"`
		Subjects []string `json:"subjects"`
		Count    int      `json:"count"`
	}

	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode log line: %v", err)
	}

	if record.Level != "WARN" || record.Count != len(want) || !slices.Equal(record.Subjects, want) {
		t.Fatalf("record = %+v, want a WARN naming %v", record, want)
	}
}
