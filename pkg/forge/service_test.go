package forge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// TestOpenRefusesAKeyForAnAddressItWasNotSealedFor sends a registration whose
// address and digest a writer of the database changed together, as if the digest
// check had passed. The key is bound to the digest it was sealed for, so it does
// not open.
func TestOpenRefusesAKeyForAnAddressItWasNotSealedFor(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)
	before := len(r.opener.calls())

	reg := r.registration(t)
	reg.BaseURL = "https://ghe.example.com/api/v3"
	reg.AddressDigest = forge.AddressDigest(reg.Kind, reg.BaseURL, reg.AppID)
	r.store.setRegistration(reg)

	_, err := r.svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrAuth) || !errors.Is(err, forge.ErrKeyNotOpened) {
		t.Fatalf("open with a changed address and digest = %v, want ErrAuth and ErrKeyNotOpened", err)
	}

	if len(r.opener.calls()) != before {
		t.Fatal("the adapter opened with a key for an address it was not sealed for")
	}

	wantDeniedOpen(t, r, forge.ReasonKeyNotOpened)
}

// wantDeniedOpen checks that the rig counted one refusal with reason and wrote
// one denied security entry for it.
func wantDeniedOpen(t *testing.T, r *rig, reason string) {
	t.Helper()

	if got := r.refusals(); len(got) != 1 || got[0] != reason {
		t.Fatalf("refusals = %v, want one %q", got, reason)
	}

	entries := r.audit.all()
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied || entries[0].Kind != audit.KindSecurity ||
		entries[0].Action != "git-host-connection:open" || !strings.HasSuffix(entries[0].Reason, reason) {
		t.Fatalf("audit entries = %+v, want one denied security entry with the reason %q", entries, reason)
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
	if !errors.Is(err, forge.ErrAuth) || !errors.Is(err, forge.ErrAddressRefused) {
		t.Fatalf("open with an address that the rules refuse = %v, want ErrAuth and ErrAddressRefused", err)
	}

	if len(r.opener.calls()) != before || r.sealer.opens != opensBefore {
		t.Fatal("the key was unsealed or the adapter opened for an address that the rules refuse")
	}

	wantDeniedOpen(t, r, forge.ReasonAddressRefused)
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
	r.advance(forge.DefaultCheckInterval)

	if _, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); !errors.Is(err, forge.ErrAuth) {
		t.Fatalf("check with a refused credential = %v, want ErrAuth", err)
	}

	entries := r.audit.all()
	if len(entries) != 2 || entries[0].Outcome != audit.OutcomeSuccess || entries[1].Outcome != audit.OutcomeFailure ||
		entries[0].Action != "git-host-connection:check" {
		t.Fatalf("entries = %+v, want a success and a failure of check", entries)
	}
}

// TestCheckWarnsWhenTheAppUsedItsBudget sends a check whose error reports that
// the app used its budget of signed calls, and a check whose error does not.
// The first writes one warning with the namespace, the connection and the
// wait; the second writes none.
func TestCheckWarnsWhenTheAppUsedItsBudget(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		checkErr error
		wantWait time.Duration
		wantLog  bool
	}{
		"the app used its budget": {
			checkErr: fmt.Errorf("%w: %w", forge.ErrAppBudget, forge.Unavailable(7*time.Second, "spent")),
			wantWait: 7 * time.Second,
			wantLog:  true,
		},
		"the budget is spent with no wait": {
			checkErr: fmt.Errorf("%w: no wait", forge.ErrAppBudget),
			wantWait: 0,
			wantLog:  true,
		},
		"the host refused the credential": {checkErr: forge.ErrAuth, wantWait: 0, wantLog: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			conn := ready(t, r)
			r.opener.checkErr = tc.checkErr

			var buf bytes.Buffer

			svc, err := forge.NewService(forge.Config{
				Store: r.store, Namespaces: newNamespaces("acme"), Secrets: r.sealer, Audit: r.audit,
				Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnAuditFailure: nil, OnRefused: nil,
				CheckLimits: nil, Logger: slog.New(slog.NewJSONHandler(&buf, nil)), Now: nil,
			})
			if err != nil {
				t.Fatalf("new service: %v", err)
			}

			_, checkErr := svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID)
			if !errors.Is(checkErr, tc.checkErr) {
				t.Fatalf("check = %v, want %v", checkErr, tc.checkErr)
			}

			records := logRecords(t, &buf)
			if !tc.wantLog {
				if len(records) != 0 {
					t.Fatalf("log records = %v, want none", records)
				}

				return
			}

			wantBudgetWarning(t, records, conn.Record.ID, tc.wantWait)
		})
	}
}

// wantBudgetWarning checks that records holds one budget warning for the
// connection, with the wait when wantWait is above zero.
func wantBudgetWarning(t *testing.T, records []map[string]any, connection string, wantWait time.Duration) {
	t.Helper()

	if len(records) != 1 {
		t.Fatalf("log records = %d, want one warning", len(records))
	}

	rec := records[0]

	const wantMessage = "The GitHub App used its budget of signed calls. " +
		"The server refused the check of the connection."

	if rec["level"] != "WARN" || rec["msg"] != wantMessage {
		t.Fatalf("record = %v, want one WARN with the message %q", rec, wantMessage)
	}

	if rec["namespace"] != "acme" || rec["connection"] != connection {
		t.Fatalf("namespace and connection = %v, %v, want acme and %s",
			rec["namespace"], rec["connection"], connection)
	}

	wantAttrs := 5 // time, level, msg, namespace, connection

	if wantWait > 0 {
		wantAttrs++

		if rec["wait"] != float64(wantWait) {
			t.Fatalf("wait = %v, want %v", rec["wait"], wantWait)
		}
	} else if _, ok := rec["wait"]; ok {
		t.Fatalf("wait = %v, want no wait attribute", rec["wait"])
	}

	if len(rec) != wantAttrs {
		t.Fatalf("record = %v, want %d attributes", rec, wantAttrs)
	}
}

// logRecords decodes the JSON records that a handler wrote to buf.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var records []map[string]any

	for line := range strings.SplitSeq(buf.String(), "\n") {
		if line == "" {
			continue
		}

		var rec map[string]any

		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode the log line %q: %v", line, err)
		}

		records = append(records, rec)
	}

	return records
}

func TestNewServiceNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	full := forge.Config{
		Store: r.store, Namespaces: newNamespaces("acme"), Secrets: r.sealer, Audit: r.audit,
		Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnAuditFailure: nil, OnRefused: nil,
		CheckLimits: nil, Logger: nil, Now: nil,
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

// failingNamespaces is a forge.Namespaces that cannot read a namespace.
type failingNamespaces struct{}

func (failingNamespaces) GetNamespace(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, errBoom
}

// TestAuditLossIsCountedWhenTheNamespaceCannotBeRead sends a refused Open and a
// Check to a service that cannot read the namespace for the fence of the audit
// entry. Each lost entry runs OnAuditFailure, and the answer does not change.
func TestAuditLossIsCountedWhenTheNamespaceCannotBeRead(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)

	var lost atomic.Int64

	svc, err := forge.NewService(forge.Config{
		Store: r.store, Namespaces: failingNamespaces{}, Secrets: r.sealer, Audit: r.audit,
		Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnRefused: nil, CheckLimits: nil,
		Logger: nil, Now: nil,
		OnAuditFailure: func() { lost.Add(1) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	before := len(r.audit.all())

	if _, checkErr := svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); checkErr != nil {
		t.Fatalf("check = %v, want the answer to stand", checkErr)
	}

	if lost.Load() != 1 {
		t.Fatalf("lost entries after a check = %d, want 1", lost.Load())
	}

	reg := r.registration(t)
	reg.BaseURL = "https://ghe.example.com/api/v3"
	r.store.setRegistration(reg)

	_, openErr := svc.Open(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(openErr, forge.ErrAddressChanged) {
		t.Fatalf("open = %v, want ErrAddressChanged", openErr)
	}

	if lost.Load() != 2 {
		t.Fatalf("lost entries after a refused open = %d, want 2", lost.Load())
	}

	if got := len(r.audit.all()); got != before {
		t.Fatalf("audit entries = %d, want %d: no entry can be written without the fence", got, before)
	}
}

// TestCheckLimitsTheCallsOfOneConnection sends a second Check at once, and then
// one after the interval, as if the route had no limit of its own.
func TestCheckLimitsTheCallsOfOneConnection(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)

	if _, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); err != nil {
		t.Fatalf("first check: %v", err)
	}

	calls := len(r.opener.calls())

	r.advance(forge.DefaultCheckInterval - time.Second)

	_, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrRateLimited) {
		t.Fatalf("second check = %v, want ErrRateLimited", err)
	}

	if wait, ok := forge.RateLimitWait(err); !ok || wait != time.Second {
		t.Fatalf("wait = %v, %v, want 1s", wait, ok)
	}

	if len(r.opener.calls()) != calls {
		t.Fatal("a refused check opened the connection")
	}

	r.advance(time.Second)

	if _, err := r.svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); err != nil {
		t.Fatalf("check after the interval: %v", err)
	}
}

// TestCheckLimitsTheCallsOfOneInstallation uses a short interval for the
// connection, so that only the limit of the installation can refuse the fourth
// Check in a minute.
func TestCheckLimitsTheCallsOfOneInstallation(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	conn := ready(t, r)
	limits := forge.CheckLimits{
		PerConnection: time.Second, PerInstallation: 3, PerApp: forge.DefaultCheckPerApp, Window: time.Minute,
	}

	svc, err := forge.NewService(forge.Config{
		Store: r.store, Namespaces: newNamespaces("acme"), Secrets: r.sealer, Audit: r.audit,
		Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnAuditFailure: nil, OnRefused: nil,
		CheckLimits: &limits, Logger: nil, Now: func() time.Time { return time.UnixMilli(r.clock.Load()) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	for i := range 3 {
		if _, checkErr := svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); checkErr != nil {
			t.Fatalf("check %d: %v", i, checkErr)
		}

		r.advance(time.Second)
	}

	_, err = svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID)
	if !errors.Is(err, forge.ErrRateLimited) {
		t.Fatalf("fourth check = %v, want ErrRateLimited", err)
	}

	if wait, ok := forge.RateLimitWait(err); !ok || wait != 57*time.Second {
		t.Fatalf("wait = %v, %v, want 57s: the first check leaves the window after a minute", wait, ok)
	}

	r.advance(57 * time.Second)

	if _, err := svc.Check(t.Context(), callerEntry(), "acme", conn.Record.ID); err != nil {
		t.Fatalf("check after the window: %v", err)
	}
}

// invalidCheckLimits are the limits that turn the limiter off or break it.
func invalidCheckLimits() map[string]forge.CheckLimits {
	good := forge.DefaultCheckLimits()

	return map[string]forge.CheckLimits{
		"zero": {PerConnection: 0, PerInstallation: 0, PerApp: good.PerApp, Window: 0},
		"zero per installation": {
			PerConnection:   good.PerConnection,
			PerInstallation: 0,
			PerApp:          good.PerApp,
			Window:          good.Window,
		},
		"negative per installation": {
			PerConnection: good.PerConnection, PerInstallation: -1, PerApp: good.PerApp, Window: good.Window,
		},
		"zero per app": {
			PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: 0, Window: good.Window,
		},
		"negative per app": {
			PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: -1, Window: good.Window,
		},
		"zero per connection": {
			PerConnection:   0,
			PerInstallation: good.PerInstallation,
			PerApp:          good.PerApp,
			Window:          good.Window,
		},
		"negative per connection": {
			PerConnection:   -time.Second,
			PerInstallation: good.PerInstallation,
			PerApp:          good.PerApp,
			Window:          good.Window,
		},
		"zero window": {
			PerConnection:   good.PerConnection,
			PerInstallation: good.PerInstallation,
			PerApp:          good.PerApp,
			Window:          0,
		},
		"negative window": {
			PerConnection:   good.PerConnection,
			PerInstallation: good.PerInstallation,
			PerApp:          good.PerApp,
			Window:          -time.Minute,
		},
	}
}

// TestNewServiceRefusesCheckLimitsThatAreNotPositive sends each limit that
// would turn the limiter off, or make the first check read an empty list.
func TestNewServiceRefusesCheckLimitsThatAreNotPositive(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	for name, limits := range invalidCheckLimits() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := forge.NewService(forge.Config{
				Store: r.store, Namespaces: newNamespaces("acme"), Secrets: r.sealer, Audit: r.audit,
				Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnAuditFailure: nil, OnRefused: nil,
				CheckLimits: &limits, Logger: nil, Now: nil,
			})
			if !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("NewService with %+v = %v, want ErrInvalid", limits, err)
			}
		})
	}
}
