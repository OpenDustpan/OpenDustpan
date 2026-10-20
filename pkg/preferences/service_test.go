package preferences_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/principal"
)

// recordingStore records the calls that reach the store.
type recordingStore struct {
	getID    string
	updateID string
	patch    preferences.Patch
	entry    audit.Entry
	calls    int
	err      error
}

func newRecordingStore(err error) *recordingStore {
	return &recordingStore{
		getID: "", updateID: "", patch: nil, entry: blankEntry(), calls: 0, err: err,
	}
}

// blankEntry returns an entry with no value set.
func blankEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: "",
		Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		PrincipalKind: "", JobID: "", Reason: "",
	}
}

func (s *recordingStore) Get(_ context.Context, id string) (preferences.Document, error) {
	s.calls++
	s.getID = id

	return preferences.Default(), s.err
}

func (s *recordingStore) Update(
	_ context.Context, id string, patch preferences.Patch, e audit.Entry,
) (preferences.Result, error) {
	s.calls++
	s.updateID, s.patch, s.entry = id, patch, e

	return preferences.Result{Document: preferences.Default(), Changed: nil}, s.err
}

// people finds principals by subject.
type people map[string]principal.Principal

func (p people) GetPrincipalBySubject(_ context.Context, subject string) (principal.Principal, error) {
	rec, ok := p[subject]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return rec, nil
}

var errPeopleDown = errors.New("people are down")

// brokenPeople fails every lookup.
type brokenPeople struct{}

func (brokenPeople) GetPrincipalBySubject(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, errPeopleDown
}

func record(id string, kind principal.Kind) principal.Principal {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: id, Subject: "",
		DisplayName: "", Kind: kind, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}
}

func callerOf(subject string, kind identity.Kind, credential identity.Credential) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: "", Admin: false, TokenID: "tok-1", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: kind, Credential: credential, JobID: "",
	}
}

func newService(store preferences.Store, p preferences.People) *preferences.Service {
	fixed := time.Date(2026, time.October, 20, 12, 0, 0, 123456789, time.FixedZone("x", 3600))

	return preferences.NewService(store, p, func() time.Time { return fixed })
}

var knownPeople = people{ //nolint:gochecknoglobals // A fixed fixture shared by the cases.
	"alice":     record("prn-alice", principal.KindPerson),
	"ci-deploy": record("prn-robot", principal.KindService),
	"a-bot":     record("prn-bot", principal.KindWorkload),
}

func TestServiceRefusesACallerThatIsNotAPerson(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		caller identity.Principal
		people preferences.People
	}{
		{
			"a service principal",
			callerOf("ci-deploy", identity.KindService, identity.CredentialAccessToken),
			knownPeople,
		},
		{"a workload", callerOf("a-bot", identity.KindWorkload, identity.CredentialAccessToken), knownPeople},
		{"a record of a service under a person kind", callerOf("ci-deploy", identity.KindPerson, ""), knownPeople},
		{
			"a job token under the subject of a person",
			callerOf("alice", identity.KindPerson, identity.CredentialJobToken),
			knownPeople,
		},
		{"a trust token", callerOf("alice", identity.KindPerson, identity.CredentialTrustToken), knownPeople},
		{"a workload kind under the subject of a person", callerOf("alice", identity.KindWorkload, ""), knownPeople},
		{"a subject with no record", callerOf("ghost", identity.KindPerson, ""), knownPeople},
		{"a bootstrap administrator", callerOf("bootstrap-admin", "", ""), knownPeople},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newRecordingStore(nil)
			svc := newService(store, tc.people)

			_, err := svc.Get(t.Context(), tc.caller)
			if !errors.Is(err, preferences.ErrNotPerson) {
				t.Errorf("Get error = %v, want ErrNotPerson", err)
			}

			_, err = svc.Update(t.Context(), tc.caller, preferences.Patch{}, blankEntry())
			if !errors.Is(err, preferences.ErrNotPerson) {
				t.Errorf("Update error = %v, want ErrNotPerson", err)
			}

			if store.calls != 0 {
				t.Errorf("store calls = %d, want 0: a caller that is not a person never reaches the store", store.calls)
			}
		})
	}
}

func TestServiceActsOnTheSubjectOfTheCaller(t *testing.T) {
	t.Parallel()

	store := newRecordingStore(nil)
	svc := newService(store, knownPeople)

	// The credential can be a session, a personal token or the token of an
	// agent run that acts as the person: the subject decides.
	for _, credential := range []identity.Credential{"", identity.CredentialSession, identity.CredentialAccessToken} {
		if _, err := svc.Get(t.Context(), callerOf("alice", identity.KindPerson, credential)); err != nil {
			t.Fatalf("Get with credential %q: %v", credential, err)
		}

		if store.getID != "prn-alice" {
			t.Errorf("store read %q, want prn-alice", store.getID)
		}
	}
}

func TestServiceSetsTheAuditEntry(t *testing.T) {
	t.Parallel()

	store := newRecordingStore(nil)
	svc := newService(store, knownPeople)

	base := blankEntry()
	base.Subject, base.TokenID, base.Action, base.Object, base.Namespace = "alice", "tok-1", "x", "y", "other"
	patch := preferences.Patch{preferences.FieldTheme: preferences.Text("terminal")}

	_, err := svc.Update(t.Context(), callerOf("alice", identity.KindPerson, ""), patch, base)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := store.entry
	if store.updateID != "prn-alice" || got.Namespace != audit.ServerScope ||
		got.Object != principal.Ref("prn-alice") ||
		got.Kind != audit.KindChange ||
		got.Outcome != audit.OutcomeSuccess ||
		got.Subject != "alice" ||
		got.TokenID != "tok-1" {
		t.Errorf(
			"entry = %+v for %q, want the scope @server, the object of alice and a success change",
			got,
			store.updateID,
		)
	}

	want := time.Date(2026, time.October, 20, 11, 0, 0, 123000000, time.UTC)
	if !got.At.Equal(want) || got.At.Location() != time.UTC {
		t.Errorf("entry time = %v, want %v: UTC, in milliseconds", got.At, want)
	}
}

// The service checks the patch again, so a caller that builds a Patch in code
// never reaches the store with a value that fails a check.
func TestServiceRefusesAHostilePatch(t *testing.T) {
	t.Parallel()

	for name, patch := range map[string]preferences.Patch{
		"updatedAt":          {"updatedAt": preferences.Text("1")},
		"terminalSchemes":    {"terminalSchemes": nil},
		"an unknown field":   {"nope": nil},
		"a bad language":     {"language": preferences.Text("ar_EG")},
		"a bad formatting":   {"formattingLocale": preferences.Text("ar-u-nu-latn")},
		"bad digits":         {"digits": preferences.Text("deva")},
		"a path as a zone":   {"timeZone": preferences.Text("../../etc/passwd")},
		"a script as scheme": {"terminalSchemes.dark": preferences.Text("<script>")},
		"an empty value":     {"theme": preferences.Text("")},
	} {
		store := newRecordingStore(nil)
		svc := newService(store, knownPeople)

		_, err := svc.Update(t.Context(), callerOf("alice", identity.KindPerson, ""), patch, blankEntry())
		if err == nil {
			t.Errorf("%s: Update = nil, want an error", name)
		}

		if store.calls != 0 {
			t.Errorf("%s: store calls = %d, want 0", name, store.calls)
		}
	}
}

func TestServiceWrapsTheErrorOfTheStore(t *testing.T) {
	t.Parallel()

	store := newRecordingStore(preferences.ErrPersonNotFound)
	svc := newService(store, knownPeople)
	alice := callerOf("alice", identity.KindPerson, "")

	if _, err := svc.Get(t.Context(), alice); !errors.Is(err, preferences.ErrPersonNotFound) {
		t.Errorf("Get error = %v, want ErrPersonNotFound", err)
	}

	_, err := svc.Update(t.Context(), alice, preferences.Patch{}, blankEntry())
	if !errors.Is(err, preferences.ErrPersonNotFound) {
		t.Errorf("Update error = %v, want ErrPersonNotFound", err)
	}
}

func TestServiceKeepsAFailureOfThePeopleStore(t *testing.T) {
	t.Parallel()

	svc := newService(newRecordingStore(nil), brokenPeople{})

	_, err := svc.Get(t.Context(), callerOf("alice", identity.KindPerson, ""))
	if !errors.Is(err, errPeopleDown) || errors.Is(err, preferences.ErrNotPerson) {
		t.Errorf("Get error = %v, want the failure of the lookup, not ErrNotPerson", err)
	}
}

func TestChangeEntry(t *testing.T) {
	t.Parallel()

	base := blankEntry()
	base.Action = "caller:manage-preferences"
	base.Changes = []audit.Change{{Key: "sessionId", Old: nil, New: "ses-1", Layer: "", TakesEffect: ""}}

	got := preferences.ChangeEntry(base, []string{"language", "terminalSchemes.dark"})

	if got.Action != preferences.AuditAction {
		t.Errorf("Action = %q, want %q", got.Action, preferences.AuditAction)
	}

	if len(got.Changes) != 3 || got.Changes[0].Key != "sessionId" || got.Changes[1].Key != "language" ||
		got.Changes[2].Key != "terminalSchemes.dark" {
		t.Errorf("Changes = %+v, want the session and the two names", got.Changes)
	}

	for _, c := range got.Changes[1:] {
		if c.Old != nil || c.New != "changed" {
			t.Errorf("change %+v, want the name only", c)
		}
	}

	if len(base.Changes) != 1 || base.Action != "caller:manage-preferences" {
		t.Errorf("the argument changed: %+v", base)
	}
}

func TestLoadAndDocumentAt(t *testing.T) {
	t.Parallel()

	settings, at, err := preferences.Load("", 0)
	if err != nil || len(settings) != 0 || !at.IsZero() {
		t.Errorf("Load of no row = %v, %v, %v; want empty settings and the zero time", settings, at, err)
	}

	settings, at, err = preferences.Load(`{"language":"he"}`, 1_760_745_600_000)
	if err != nil || at.UnixMilli() != 1_760_745_600_000 || at.Location() != time.UTC {
		t.Fatalf("Load = %v, %v, %v", settings, at, err)
	}

	if doc := settings.DocumentAt(at); doc.Language != "he" || !doc.UpdatedAt.Equal(at) {
		t.Errorf("DocumentAt = %+v", doc)
	}

	if _, _, err := preferences.Load(`{"language":"x y"}`, 1); !errors.Is(err, preferences.ErrInvalidStored) {
		t.Errorf("Load of a bad document error = %v, want ErrInvalidStored", err)
	}
}
