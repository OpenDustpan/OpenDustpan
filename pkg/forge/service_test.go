package forge_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

func TestOpenUsesTheAddressAndTheAppIDOfTheRegistration(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)

	if _, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID); err != nil {
		t.Fatalf("open: %v", err)
	}

	calls := r.opener.calls()
	// The first call is the probe of the key at registration. The last is Open.
	got := calls[len(calls)-1]

	if got.Conn.BaseURL != apiURL || got.Conn.Params[forge.ParamAppID] != appID ||
		got.Conn.Params[forge.ParamInstallationID] != "101" || got.Conn.Auth != forge.AuthApp {
		t.Fatalf("open call = %+v, want the address and app ID of the registration", got.Conn)
	}

	if string(got.Cred.Secret) != keyCanary {
		t.Fatal("the adapter did not get the unsealed key")
	}
}

func TestOpenRefusesAnInstallationThatTheMapDoesNotAssign(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)
	before := len(r.opener.calls())

	if err := r.svc.UnassignInstallation(t.Context(), callerEntry(), "101", 0); err != nil {
		t.Fatalf("unassign: %v", err)
	}

	opensBefore := r.sealer.opens

	_, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrAuth) || !errors.Is(err, forge.ErrInstallationNotAssigned) {
		t.Fatalf("open after unassign = %v, want ErrAuth and ErrInstallationNotAssigned", err)
	}

	if len(r.opener.calls()) != before {
		t.Fatal("the adapter was opened for a refused connection")
	}

	if r.sealer.opens != opensBefore {
		t.Fatal("the key was unsealed for a refused connection")
	}

	if got := r.refusals(); len(got) != 1 || got[0] != forge.ReasonInstallationNotAssigned {
		t.Fatalf("refusals = %v, want one %q", got, forge.ReasonInstallationNotAssigned)
	}

	entries := r.audit.all()
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied || entries[0].Kind != audit.KindSecurity ||
		entries[0].Action != "git-host-connection:open" {
		t.Fatalf("audit entries = %+v, want one denied security entry", entries)
	}
}

func TestOpenRefusesAfterTheOperatorMovesTheInstallation(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)

	r.assign(t, "101", "beta")

	_, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("open after the move = %v, want ErrAuth", err)
	}
}

func TestOpenFailsClosedWhenTheMapCannotBeRead(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)
	before := len(r.opener.calls())

	r.store.mu.Lock()
	r.store.failAssignment = true
	r.store.mu.Unlock()

	_, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("open with an unreadable map = %v, want ErrAuth", err)
	}

	if len(r.opener.calls()) != before {
		t.Fatal("the adapter was opened while the map was unreadable")
	}

	if got := r.refusals(); len(got) != 1 || got[0] != forge.ReasonInstallationNotAssigned {
		t.Fatalf("refusals = %v, want one %q", got, forge.ReasonInstallationNotAssigned)
	}
}

func TestOpenRefusesACredentialWhoseAddressChanged(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)
	before := len(r.opener.calls())
	opensBefore := r.sealer.opens

	// A writer of the database changes the address and keeps the old digest.
	reg := r.registration(t)
	reg.BaseURL = "https://evil.example.com"
	r.store.setRegistration(reg)

	_, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrAuth) || !errors.Is(err, forge.ErrAddressChanged) {
		t.Fatalf("open after the address changed = %v, want ErrAuth and ErrAddressChanged", err)
	}

	if len(r.opener.calls()) != before || r.sealer.opens != opensBefore {
		t.Fatal("the key was unsealed or the adapter opened for an address that the digest does not cover")
	}

	if got := r.refusals(); len(got) != 1 || got[0] != forge.ReasonAddressChanged {
		t.Fatalf("refusals = %v, want one %q", got, forge.ReasonAddressChanged)
	}
}

func TestOpenAppliesTheAddressRulesAtEachCall(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)
	before := len(r.opener.calls())
	opensBefore := r.sealer.opens

	// The digest covers the new address, so only the address rules can refuse it.
	reg := r.registration(t)
	reg.BaseURL = "https://127.0.0.1"
	reg.AddressDigest = forge.AddressDigest(reg.Kind, reg.BaseURL, reg.AppID)
	r.store.setRegistration(reg)

	_, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("open with an address that the rules refuse = %v, want ErrInvalid", err)
	}

	if len(r.opener.calls()) != before || r.sealer.opens != opensBefore {
		t.Fatal("the key was unsealed or the adapter opened for an address that the rules refuse")
	}
}

func TestOpenRefusesAnUnknownOrForeignConnection(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)

	for name, tc := range map[string]struct{ ns, id string }{
		"an unknown ID":       {"acme", forge.NewConnectionID()},
		"another namespace":   {"beta", conn.Record.ID},
		"an ID that is wrong": {"acme", "../../etc/passwd"},
	} {
		_, err := r.svc.Open(t.Context(), callerEntry(), mustName(tc.ns), tc.id)
		if !errors.Is(err, forge.ErrConnectionNotFound) {
			t.Errorf("open of %s = %v, want ErrConnectionNotFound", name, err)
		}
	}
}

func TestCreateConnectionRefusesWhatTheOperatorDidNotSet(t *testing.T) {
	t.Parallel()

	in := connectionInput("101")

	t.Run("no registration", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)

		_, err := r.svc.CreateConnection(t.Context(), callerEntry(), "acme", in)
		if !errors.Is(err, forge.ErrNoRegistration) {
			t.Fatalf("create with no registration = %v, want ErrNoRegistration", err)
		}
	})

	t.Run("an installation that no one assigned", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		r.register(t)

		_, err := r.svc.CreateConnection(t.Context(), callerEntry(), "acme", in)
		if !errors.Is(err, forge.ErrInstallationNotAssigned) {
			t.Fatalf("create = %v, want ErrInstallationNotAssigned", err)
		}
	})

	t.Run("an installation of another namespace", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)
		r.register(t)
		r.assign(t, "101", "beta")

		_, err := r.svc.CreateConnection(t.Context(), callerEntry(), "acme", in)
		if !errors.Is(err, forge.ErrInstallationNotAssigned) {
			t.Fatalf("create = %v, want ErrInstallationNotAssigned", err)
		}
	})

	t.Run("a namespace that does not exist", func(t *testing.T) {
		t.Parallel()

		r := newRig(t)

		if _, err := r.svc.CreateConnection(t.Context(), callerEntry(), "nowhere", in); err == nil {
			t.Fatal("create in a missing namespace passed")
		}
	})
}

func TestCreateConnectionRefusesInputThatIsNotValid(t *testing.T) {
	t.Parallel()

	for name, edit := range map[string]func(c *forge.ConnectionInput){
		"another kind":       func(c *forge.ConnectionInput) { c.Kind = forge.KindGitLab },
		"the token auth":     func(c *forge.ConnectionInput) { c.Auth = forge.AuthToken },
		"a bad installation": func(c *forge.ConnectionInput) { c.InstallationID = "1 OR 1=1" },
		"a leading zero":     func(c *forge.ConnectionInput) { c.InstallationID = "0101" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			bad := connectionInput("101")
			edit(&bad)

			_, err := r.svc.CreateConnection(t.Context(), callerEntry(), "acme", bad)
			if !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("create = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestPutRegistrationRefusesInputThatIsNotValid(t *testing.T) {
	t.Parallel()

	for name, edit := range map[string]func(in *forge.RegistrationInput){
		"an event secret of 31 bytes": func(in *forge.RegistrationInput) { in.EventSecret = bytes.Repeat([]byte("a"), 31) },
		"no event secret":             func(in *forge.RegistrationInput) { in.EventSecret = nil },
		"an address that the rules refuse": func(in *forge.RegistrationInput) {
			in.BaseURL = "https://169.254.169.254"
		},
		"an address with the http scheme": func(in *forge.RegistrationInput) { in.BaseURL = "http://api.github.com" },
		"a bad app ID":                    func(in *forge.RegistrationInput) { in.AppID = "12a" },
		"no app ID":                       func(in *forge.RegistrationInput) { in.AppID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			in := registrationInput()
			edit(&in)

			_, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, in, 0)
			if !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("put = %v, want ErrInvalid", err)
			}

			if r.sealer.seals != 0 || len(r.store.audits) != 0 {
				t.Fatal("a refused registration was sealed or stored")
			}
		})
	}
}

func TestPutRegistrationRefusesAKeyThatTheAdapterCannotParse(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.opener.openErr = fmt.Errorf("%w: the key is not a PEM block", forge.ErrInvalid)

	_, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, registrationInput(), 0)
	if !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("put with a bad key = %v, want ErrInvalid", err)
	}

	if r.sealer.seals != 0 {
		t.Fatal("a refused key was sealed")
	}
}

func TestPutRegistrationRefusesAKindThatHasNoAdapter(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	_, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitLab, registrationInput(), 0)
	if !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("put of another kind = %v, want ErrInvalid", err)
	}
}

func TestPutRegistrationAcceptsAnEventSecretOf32Bytes(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	in := registrationInput()
	in.EventSecret = bytes.Repeat([]byte("a"), forge.MinEventSecretBytes)

	if _, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, in, 0); err != nil {
		t.Fatalf("put with 32 bytes: %v", err)
	}
}

func TestPutRegistrationKeepsTheHookIDAndSealsTheSecrets(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	first := r.register(t)

	in := registrationInput()
	in.AppID = "777"

	second, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, in, first.Version)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	if second.HookID != first.HookID || second.Version != first.Version+1 || second.AppID != "777" {
		t.Fatalf("replace = %+v, want the hook ID %s at the next version", second, first.HookID)
	}

	reg := r.registration(t)
	if bytes.Contains(reg.SealedKey, []byte(keyCanary)) || bytes.Contains(reg.SealedEventSecret, []byte(secretCanary)) {
		t.Fatal("the store holds a secret that is not sealed")
	}
}

// TestNoSecretReachesAnAuditEntryAnAnswerOrAnError sends canaries as the key and
// the event secret, and looks for them in every place that a person or an
// operator can read.
func TestNoSecretReachesAnAuditEntryAnAnswerOrAnError(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	view := r.register(t)
	r.assign(t, "101", "acme")
	conn := r.connect(t, "acme", "101")

	got, err := r.svc.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get registration: %v", err)
	}

	list, err := r.svc.ListConnections(t.Context(), "acme")
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}

	r.opener.checkErr = forge.ErrAuth
	_, checkErr := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID)

	texts := []string{fmt.Sprintf("%+v", view), fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", list)}
	if checkErr != nil {
		texts = append(texts, checkErr.Error())
	}

	for _, e := range append(r.audit.all(), r.store.audits...) {
		texts = append(texts, fmt.Sprintf("%+v", e))
	}

	for _, text := range texts {
		for _, canary := range []string{keyCanary, secretCanary} {
			if strings.Contains(text, canary) {
				t.Fatalf("a secret reached %q", text)
			}
		}
	}
}

func TestCheckAnswersTheAccountAndTheCapabilities(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.opener.caps = forge.Capabilities{forge.CapFiles, forge.CapCompare, forge.CapCommit}
	conn := ready(t, r)

	res, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if res.Account.Login != "acme-org" || res.Account.ID != "99" {
		t.Fatalf("account = %+v", res.Account)
	}

	if len(res.Capabilities) != 3 || !res.Capabilities.Has(forge.CapCommit) {
		t.Fatalf("capabilities = %v", res.Capabilities)
	}
}

func TestCheckNamesWhatEachConsumerMisses(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.opener.caps = forge.Capabilities{forge.CapFiles, forge.CapCompare, forge.CapCommit}
	conn := ready(t, r)

	res, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	missing := map[forge.Consumer]forge.Capabilities{}
	for _, n := range res.Needs {
		missing[n.Consumer] = n.Missing
	}

	if len(missing) != len(forge.Consumers()) {
		t.Fatalf("needs = %v, want one row for each consumer", res.Needs)
	}

	if len(missing[forge.ConsumerWriteBack]) != 0 {
		t.Fatalf("write-back misses %v, want nothing", missing[forge.ConsumerWriteBack])
	}

	if got := missing[forge.ConsumerWriteBackReview]; len(got) != 1 || got[0] != forge.CapReview {
		t.Fatalf("write-back-review misses %v, want review", got)
	}

	if got := missing[forge.ConsumerRunnerGit]; len(got) != 1 || got[0] != forge.CapRepoToken {
		t.Fatalf("runner-git misses %v, want repo-token", got)
	}
}

func TestCheckAuditsItsOutcome(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)

	if _, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); err != nil {
		t.Fatalf("check: %v", err)
	}

	r.opener.checkErr = forge.ErrAuth

	if _, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("check with a refused credential = %v, want ErrAuth", err)
	}

	entries := r.audit.all()
	if len(entries) != 2 || entries[0].Outcome != audit.OutcomeSuccess || entries[1].Outcome != audit.OutcomeFailure ||
		entries[0].Action != "git-host-connection:check" {
		t.Fatalf("entries = %+v, want a success and a failure of check", entries)
	}
}

func TestNewServiceNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	full := forge.Config{
		Store: r.store, Namespaces: newNamespaces("acme"), Secrets: r.sealer, Audit: r.audit,
		Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnAuditFailure: nil, OnRefused: nil, Now: nil,
	}

	for name, edit := range map[string]func(c *forge.Config){
		"store":      func(c *forge.Config) { c.Store = nil },
		"namespaces": func(c *forge.Config) { c.Namespaces = nil },
		"secrets":    func(c *forge.Config) { c.Secrets = nil },
		"audit":      func(c *forge.Config) { c.Audit = nil },
		"check":      func(c *forge.Config) { c.CheckURL = nil },
		"openers":    func(c *forge.Config) { c.Openers = nil },
	} {
		cfg := full
		edit(&cfg)

		if _, err := forge.NewService(cfg); !errors.Is(err, forge.ErrMissingDependency) {
			t.Errorf("service without %s = %v, want ErrMissingDependency", name, err)
		}
	}
}

func TestListAssignmentsIsOrderedByNumber(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	for _, id := range []string{"100", "9", "20"} {
		r.assign(t, id, "acme")
	}

	list, err := r.svc.ListAssignments(t.Context())
	if err != nil || len(list) != 3 || list[0].InstallationID != "9" || list[2].InstallationID != "100" {
		t.Fatalf("assignments = %+v, %v; want 9, 20, 100", list, err)
	}
}

func TestAnIDThatIsNotValidIsNotFound(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	ready(t, r)

	for _, bad := range []string{"", "x", "gitconn-xyz", "gitconn-" + strings.Repeat("g", 32)} {
		if _, err := r.svc.GetConnection(t.Context(), "acme", bad); !errors.Is(err, forge.ErrConnectionNotFound) {
			t.Errorf("get %q = %v, want ErrConnectionNotFound", bad, err)
		}

		err := r.svc.DeleteConnection(t.Context(), callerEntry(), "acme", bad, 0)
		if !errors.Is(err, forge.ErrConnectionNotFound) {
			t.Errorf("delete %q = %v, want ErrConnectionNotFound", bad, err)
		}
	}

	if _, err := r.svc.GetAssignment(t.Context(), "abc"); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Errorf("get assignment abc = %v, want ErrAssignmentNotFound", err)
	}
}
