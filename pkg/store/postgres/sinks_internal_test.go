package postgres

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
)

// sinkPlain is the plaintext value of the secret item that seedSink stores.
const sinkPlain = "https://hooks.slack.com/services/T0/B0/PLAINTEXT-CANARY"

// sinkEntry returns a valid audit entry for a write of a sink in acme.
func sinkEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "ana", IDPSubject: "", TokenID: "", Action: "sink:create",
		Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// seedSink makes the namespace acme and one chat sink in it. The secret item
// is the sealed URL of the webhook.
func seedSink(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	sink := notify.Sink{
		ID:         "snk-aaaaaaaa",
		Scope:      "acme",
		Kind:       notify.SinkChat,
		Name:       "Ops",
		Platform:   "slack",
		Target:     "hooks.slack.com",
		SecretRefs: []string{"itm-aaaaaaaa"},
		CreatedBy:  "ana",
		Version:    0,
	}

	sealed, err := notifytest.Sealer().Seal(t.Context(), sink, []notify.SinkSecret{
		{Ref: "itm-aaaaaaaa", Value: []byte(sinkPlain)},
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if err = s.NotificationSinks().PutSink(t.Context(), sink, sealed, ns.Fence(), sinkEntry()); err != nil {
		t.Fatalf("put sink: %v", err)
	}
}

// sinkTampering is a change of the stored rows. served is true when the sink
// stays readable: the change breaks a rule that only the restore check holds.
type sinkTampering struct {
	name   string
	update string
	served bool
}

// sinkTamperings lists the changes that make the stored sink tables fail the
// rules of a stored sink.
func sinkTamperings() []sinkTampering {
	return []sinkTampering{
		{"unknown kind", `UPDATE notification_sinks SET kind = 'pager'`, false},
		{"chat sink with no platform", `UPDATE notification_sinks SET platform = ''`, false},
		{"chat sink with an unknown platform", `UPDATE notification_sinks SET platform = 'irc'`, false},
		{"chat sink with a loopback host", `UPDATE notification_sinks SET target = '127.0.0.1'`, false},
		{"chat sink with a URL as target", `UPDATE notification_sinks SET target = 'https://hooks.slack.com/x'`, false},
		{
			"webhook with a platform",
			`UPDATE notification_sinks SET kind = 'webhook', target = 'https://a.example.com/'`,
			false,
		},
		{"version 0", `UPDATE notification_sinks SET version = 0`, false},
		{"empty name", `UPDATE notification_sinks SET name = ''`, false},
		{"ID without the prefix", `UPDATE notification_sinks SET id = 'sink-1'`, false},
		{"secret of another sink", `UPDATE notification_sink_secrets SET sink_id = 'snk-bbbbbbbb'`, false},
		{"secret ref that the namespace never held", `DELETE FROM notification_sink_refs`, true},
	}
}

// TestCheckStagedSinks proves the restore check: it accepts the rows that the
// store wrote, and it refuses each row that a stored sink would not pass. A
// sink that fails is also not served by a read.
func TestCheckStagedSinks(t *testing.T) {
	t.Parallel()

	t.Run("rows that the store wrote pass", func(t *testing.T) {
		t.Parallel()

		s := openForSavedViews(t)
		seedSink(t, s)

		if err := checkStagedSinks(t.Context(), s.pool, currentSchema(t, s)); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range sinkTamperings() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkTamperedSink(t, tc)
		})
	}
}

// checkTamperedSink changes the stored rows as tc says, and checks that the
// restore check refuses them. Unless the sink stays readable, it also checks
// that a read does not serve the sink: the read path checks the row again.
func checkTamperedSink(t *testing.T, tc sinkTampering) {
	t.Helper()

	s := openForSavedViews(t)
	seedSink(t, s)

	if _, err := s.pool.Exec(t.Context(), tc.update); err != nil {
		t.Fatalf("change the rows: %v", err)
	}

	if err := checkStagedSinks(t.Context(), s.pool, currentSchema(t, s)); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if tc.served {
		return
	}

	var logs bytes.Buffer

	s.logger = slog.New(slog.NewTextHandler(&logs, nil))

	got, err := s.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(got) != 0 {
		t.Fatalf("Sinks of a bad row = %+v, %v; want none", got, err)
	}

	// The warning names the row, and shows no value of it.
	if !strings.Contains(logs.String(), "left out") || !strings.Contains(logs.String(), "acme") {
		t.Errorf("log = %q, want a warning that names the namespace", logs.String())
	}

	for _, value := range []string{"hooks.slack.com", "127.0.0.1", "pager", "irc", "Ops"} {
		if strings.Contains(logs.String(), value) {
			t.Errorf("log = %q, must not show %q", logs.String(), value)
		}
	}
}

// legacyHostSinks lists the stored rows whose host was valid before the host
// allowlist of a write: the update changes the seeded chat sink.
func legacyHostSinks() []sinkTampering {
	return []sinkTampering{
		{"chat host in raw Unicode", `UPDATE notification_sinks SET target = 'chat.müller.de'`, true},
		{"chat host with a trailing dot", `UPDATE notification_sinks SET target = 'mm.example.com.'`, true},
		{"chat host with an underscore", `UPDATE notification_sinks SET target = 'mm_chat.example.com'`, true},
		{"chat host with an empty port", `UPDATE notification_sinks SET target = 'hooks.example.com:'`, true},
		{
			"webhook host with an underscore",
			`UPDATE notification_sinks SET kind = 'webhook', platform = '', target = 'hk_1.example.com';
				INSERT INTO notification_sink_refs (scope, ref) SELECT scope, 'itm-bbbbbbbb' FROM notification_sinks;
				INSERT INTO notification_sink_secrets (scope, ref, sink_id, position, ciphertext)
					SELECT scope, 'itm-bbbbbbbb', id, 1, '\x00'::bytea FROM notification_sinks`,
			true,
		},
	}
}

// TestStoredSinkFromBeforeTheHostCheck proves that a stored sink whose host
// fails the allowlist of a write still passes the restore check and is served
// by a read, with the flag that asks for a new host. A write of it is refused.
func TestStoredSinkFromBeforeTheHostCheck(t *testing.T) {
	t.Parallel()

	for _, tc := range legacyHostSinks() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedSink(t, s)

			if _, err := s.pool.Exec(t.Context(), tc.update); err != nil {
				t.Fatalf("change the rows: %v", err)
			}

			if err := checkStagedSinks(t.Context(), s.pool, currentSchema(t, s)); err != nil {
				t.Fatalf("restore check = %v, want nil", err)
			}

			checkServedAndNotWritten(t, s)
		})
	}
}

// checkServedAndNotWritten checks that a read serves the stored sink with the
// flag, with no warning in the log, and that a write of it is refused.
func checkServedAndNotWritten(t *testing.T, s *Store) {
	t.Helper()

	var logs bytes.Buffer

	s.logger = slog.New(slog.NewTextHandler(&logs, nil))

	got, err := s.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(got) != 1 || !got[0].HostNeedsUpdate() {
		t.Fatalf("Sinks = %+v, %v; want the sink, flagged for a new host", got, err)
	}

	// The write path keeps the allowlist: the same sink is not stored again.
	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	err = s.NotificationSinks().PutSink(t.Context(), got[0], nil, fence, sinkEntry())
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("PutSink of the sink = %v, want ErrInvalid", err)
	}

	if logs.Len() != 0 {
		t.Errorf("log = %q, want no warning: the sink is served", logs.String())
	}
}

// scopeGoneStatement returns the statement that points the rows of a sink
// table at a scope that names no namespace.
func scopeGoneStatement(table string) string {
	return map[string]string{
		"notification_sinks":        `UPDATE notification_sinks SET scope = 'gone'`,
		"notification_sink_secrets": `UPDATE notification_sink_secrets SET scope = 'gone'`,
		"notification_sink_refs":    `UPDATE notification_sink_refs SET scope = 'gone'`,
	}[table]
}

// TestCheckStagedSinksRefusesAMissingNamespace proves that a sink, a secret
// item and a held ref whose scope names no namespace of the backup are
// corrupt, so the restore never brings in a row that no namespace owns.
func TestCheckStagedSinksRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	for _, table := range []string{"notification_sinks", "notification_sink_secrets", "notification_sink_refs"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedSink(t, s)

			stmt := scopeGoneStatement(table)
			if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
				t.Fatalf("%s: %v", stmt, execErr)
			}

			err := checkStagedNotifications(t.Context(), s.pool, currentSchema(t, s))
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestSinkSecretIsSealedInTheDatabase proves that the column of the secret
// item holds the sealed value the store got, and never the plaintext.
func TestSinkSecretIsSealedInTheDatabase(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedSink(t, s)

	var stored []byte

	err := s.pool.QueryRow(t.Context(), `SELECT ciphertext FROM notification_sink_secrets`).
		Scan(&stored)
	if err != nil {
		t.Fatalf("read the secret: %v", err)
	}

	if len(stored) == 0 || bytes.Contains(stored, []byte("PLAINTEXT-CANARY")) ||
		bytes.Contains(stored, []byte("hooks.slack.com")) {
		t.Fatalf("stored secret = %q, want a sealed value", stored)
	}

	// The sealed value opens for its own sink and for no other.
	sinks, err := s.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(sinks) != 1 {
		t.Fatalf("Sinks = %+v, %v; want one", sinks, err)
	}

	sealed, err := s.NotificationSinks().SinkSecrets(t.Context(), "acme", sinks[0].ID)
	if err != nil {
		t.Fatalf("SinkSecrets: %v", err)
	}

	values, err := notifytest.Sealer().Open(t.Context(), sinks[0], sealed)
	if err != nil || string(values[0]) != sinkPlain {
		t.Fatalf("Open = %q, %v; want the plaintext back", values, err)
	}
}

// TestCheckStagedSinksRefusesASecretOfAnotherScope proves that a secret item in
// one scope that names a sink of another scope is corrupt, so a restore never
// brings in a value that the sink of another scope would open.
func TestCheckStagedSinksRefusesASecretOfAnotherScope(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedSink(t, s)

	for _, stmt := range []string{
		`INSERT INTO notification_sink_refs (scope, ref) VALUES ('@server', 'itm-zzzzzzzz')`,
		`INSERT INTO notification_sink_secrets (scope, ref, sink_id, position, ciphertext)
			VALUES ('@server', 'itm-zzzzzzzz', 'snk-aaaaaaaa', 0, '\x00'::bytea)`,
	} {
		if _, err := s.pool.Exec(t.Context(), stmt); err != nil {
			t.Fatalf("plant the row: %v", err)
		}
	}

	if err := checkStagedSinks(t.Context(), s.pool, currentSchema(t, s)); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}
}
