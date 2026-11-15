package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// serverAuditWriters is the count of the writers that one case starts at the
// same time.
const serverAuditWriters = 16

// serverAuditAt returns the fixed time of each entry of these tests, so the
// tests read no clock.
func serverAuditAt() time.Time {
	return time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
}

// TestServerAuditConcurrentWriters starts 16 writers of one caller family at
// the same time. Each write must succeed, and the chain of @server must
// check: the store holds the scope lock for each append, so two appends
// never read the same last entry.
func TestServerAuditConcurrentWriters(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL concurrency suite", envURL)
	}

	cases := []struct {
		setup func(t *testing.T, s *postgres.Store)
		write func(ctx context.Context, s *postgres.Store, i int) error
		name  string
	}{
		{name: "provider configs", setup: nil, write: writeServerProviderConfig},
		{name: "passkey use with a counter regression", setup: setServerPasskeys, write: writeServerPasskeyUse},
		{name: "generation bump", setup: setServerGenerationPersons, write: writeServerGenerationBump},
		{name: "name locks", setup: nil, write: writeServerNameLockClear},
		{name: "access tokens", setup: setServerTokenPerson, write: writeServerAccessToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := open(t, base)

			if tc.setup != nil {
				tc.setup(t, s)
			}

			runServerAuditWriters(t, s, tc.write)
		})
	}
}

// runServerAuditWriters starts serverAuditWriters writers of write at the
// same time, waits for them, checks each error, and checks the chain of
// @server.
func runServerAuditWriters(
	t *testing.T,
	s *postgres.Store,
	write func(ctx context.Context, s *postgres.Store, i int) error,
) {
	t.Helper()

	errs := make([]error, serverAuditWriters)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := range serverAuditWriters {
		wg.Go(func() {
			<-start

			errs[i] = write(t.Context(), s, i)
		})
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	invalid, err := audit.CheckScopes(t.Context(), s, []namespace.Name{audit.ServerScope})
	if err != nil {
		t.Fatalf("check the server audit scope: %v", err)
	}

	if invalid != 0 {
		t.Fatalf("the server audit chain holds %d breaks", invalid)
	}
}

// seedServerPerson creates the person principal id.
func seedServerPerson(t *testing.T, s *postgres.Store, id string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt:   serverAuditAt(),
		UpdatedAt:   serverAuditAt(),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     "local:" + id,
		DisplayName: "Person " + id,
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	if err := s.CreatePrincipal(t.Context(), p, storetest.ServerChange(serverAuditAt(), "principal:"+id)); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// writeServerProviderConfig creates the sign-in provider config i.
func writeServerProviderConfig(ctx context.Context, s *postgres.Store, i int) error {
	id := "corp-" + strconv.Itoa(i)

	p := session.ProviderConfig{
		CreatedAt:    serverAuditAt(),
		UpdatedAt:    serverAuditAt(),
		ID:           id,
		Mode:         session.ModeOIDC,
		Issuer:       "https://" + id + ".idp.example",
		DisplayName:  "Corp " + id,
		ClientID:     "dustpan-" + id,
		SealedSecret: []byte("sealed secret"),
		Scopes:       []string{"profile"},
		OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "", UsernameClaim: "",
			EmailClaim: "",
		},
		Version:    1,
		AutoCreate: true,
		Enabled:    false,
	}

	return s.CreateProviderConfig(ctx, p, storetest.ServerChange(serverAuditAt(), "sign-in-provider:"+id))
}

// serverPasskeyID returns the ID of the passkey i of the passkey case.
func serverPasskeyID(i int) string {
	return "pky-" + strconv.Itoa(i)
}

// setServerPasskeys creates the person of the passkey case and the passkey
// of each writer, with the stored counter 5.
func setServerPasskeys(t *testing.T, s *postgres.Store) {
	t.Helper()

	const personID = "prn-server-passkey-lock"

	seedServerPerson(t, s, personID)

	for i := range serverAuditWriters {
		id := serverPasskeyID(i)

		p := mfa.Passkey{
			CreatedAt:      serverAuditAt(),
			LastUsedAt:     time.Time{},
			SuspendedAt:    time.Time{},
			ID:             id,
			PersonID:       personID,
			Name:           "key-" + strconv.Itoa(i),
			CredentialID:   []byte("credential-" + strconv.Itoa(i)),
			PublicKey:      []byte("cose-key-" + strconv.Itoa(i)),
			Transports:     []string{mfa.TransportUSB},
			AAGUID:         [16]byte{},
			SignCount:      5,
			BackupEligible: false,
		}

		if err := s.AddPasskey(t.Context(), p, storetest.ServerChange(serverAuditAt(), "passkey:"+id)); err != nil {
			t.Fatalf("add passkey %d: %v", i, err)
		}
	}
}

// writeServerPasskeyUse records a use of the passkey i with the counter 0,
// below the stored 5: the store suspends the passkey, appends the entry of
// the suspension, and answers mfa.ErrCounterRegressed. That answer is the
// success of the case; a conflict of two appends is not.
func writeServerPasskeyUse(ctx context.Context, s *postgres.Store, i int) error {
	id := serverPasskeyID(i)

	err := s.RecordUse(ctx,
		mfa.PasskeyUse{At: serverAuditAt(), PasskeyID: id, SignCount: 0},
		storetest.ServerChange(serverAuditAt(), "passkey:"+id))
	if errors.Is(err, mfa.ErrCounterRegressed) {
		return nil
	}

	return err
}

// serverGenerationPersonID returns the ID of the person i of the generation
// case.
func serverGenerationPersonID(i int) string {
	return "prn-generation-" + strconv.Itoa(i)
}

// setServerGenerationPersons creates the person of each writer of the
// generation case, so the writers lock different rows.
func setServerGenerationPersons(t *testing.T, s *postgres.Store) {
	t.Helper()

	for i := range serverAuditWriters {
		seedServerPerson(t, s, serverGenerationPersonID(i))
	}
}

// writeServerGenerationBump bumps the session generation of the person i.
func writeServerGenerationBump(ctx context.Context, s *postgres.Store, i int) error {
	id := serverGenerationPersonID(i)

	_, err := s.BumpGeneration(ctx, id, session.BumpPassword, 0,
		storetest.ServerChange(serverAuditAt(), "session-generation:"+id))

	return err
}

// writeServerNameLockClear clears the name counts of the name i, with the
// entry of the clear.
func writeServerNameLockClear(ctx context.Context, s *postgres.Store, i int) error {
	name := "server-name-" + strconv.Itoa(i)

	_, err := s.ClearName(ctx, []string{authgate.ScopePassword}, name,
		storetest.ServerChange(serverAuditAt(), "name-lock:"+name))

	return err
}

// serverOwnerID is the person that owns the access tokens of the last case.
const serverOwnerID = "prn-server-owner"

// setServerTokenPerson creates the person that owns the access tokens of the
// token case.
func setServerTokenPerson(t *testing.T, s *postgres.Store) {
	t.Helper()

	seedServerPerson(t, s, serverOwnerID)
}

// writeServerAccessToken creates the access token i of the person.
func writeServerAccessToken(ctx context.Context, s *postgres.Store, i int) error {
	id := "tok-" + strconv.Itoa(i)

	token := accesstoken.Token{
		CreatedAt:   serverAuditAt(),
		ExpiresAt:   serverAuditAt().Add(time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: serverOwnerID,
		Name:        "token-" + strconv.Itoa(i),
		Kind:        accesstoken.KindPersonal,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}

	d := accesstoken.Digest(sha256.Sum256([]byte(id)))

	return s.CreateToken(ctx, token, d, storetest.ServerChange(serverAuditAt(), "token:"+id))
}
