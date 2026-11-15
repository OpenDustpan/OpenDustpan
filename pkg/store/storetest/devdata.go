package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
)

// DevDataStore is every port the dev data suite exercises: the restore ports
// with the portable export and stage, the access-keeping swap, the three dev
// data ports of backup.RestoreLive, and the claim of the installation kind.
type DevDataStore interface {
	PortableRestoreStore
	backup.AccessKeepingApplier
	backup.KindReader
	backup.DevStripper
	installation.KindStore
}

// DevDataFactory returns a fresh store for one subtest. A non-empty kind is
// claimed before the store returns. An empty kind claims nothing, as a
// database that no command started on.
type DevDataFactory func(t *testing.T, kind installation.Kind) DevDataStore

// KindEraser is an optional capability of a DevDataStore: it sets the stored
// installation kind back to NULL, behind the back of the port, so the suite
// can prove that the swap refuses when it cannot read the live kind. A store
// without it skips that case.
type KindEraser interface {
	EraseKindForTest(ctx context.Context) error
}

// RunDevData executes the conformance suite of the dev data ports against
// stores produced by newStore: the kind reader, the kind check of the stage
// call, the strip of the staged data, the report in the restore record, and
// the check of the swap.
func RunDevData(t *testing.T, newStore DevDataFactory) {
	t.Helper()

	env := &devEnv{newStore: newStore, once: sync.Once{}, seeded: nil}

	cases := []struct {
		test func(t *testing.T, env *devEnv)
		name string
	}{
		{name: "the kind reader returns the claimed kind and claims none", test: testDevReadKind},
		{name: "the stage call refuses a dev backup into production by itself", test: testDevStageKind},
		{name: "the strip removes the dev data and nothing else", test: testDevStrip},
		{name: "the strip matches the shared table of subjects", test: testDevStripMatchesSharedTable},
		{name: "the strip fails closed on a hostile approvers body", test: testDevStripHostileBodies},
		{name: "the strip bounds the report and cleans the subjects", test: testDevStripBounds},
		{name: "detect changes nothing and remove proves the staging area clean", test: testDevStripModes},
		{name: "the swap refuses dev data by itself", test: testDevSwapRefuses},
		{name: "the swap of a dev installation takes dev data", test: testDevSwapDevInstallation},
		{name: "the swap refuses when it cannot read the live kind", test: testDevSwapUnreadableKind},
		{
			name: "a swap that keeps access drops temp admins that dangle against the live principals",
			test: testDevKeepDropsTempAdmins,
		},
		{name: "the strip and the swap cover the whole staging area on the keep path", test: testDevKeepWholeArea},
		{name: "the report is stored in the restore record", test: testDevRecordReport},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, env)
		})
	}
}

// devExport is a portable export of a seeded production store, with the
// edits of one case.
type devExport struct {
	files map[string][]byte
	descs []backup.PortableFile
	sum   backup.Summary
}

// devEnv is the state that the cases of one run share: the factory of stores,
// and the export of the seeded production store. The export is plain bytes and
// no case changes it (with returns a copy), so the cases read one export
// instead of seeding and exporting a store each.
type devEnv struct {
	newStore DevDataFactory
	once     sync.Once
	seeded   *devExport
}

// export returns the portable export of a seeded production store. The first
// call seeds a store and exports it.
func (env *devEnv) export(t *testing.T) *devExport {
	t.Helper()

	env.once.Do(func() {
		src := env.newStore(t, installation.KindProduction)
		seedBackupStore(t, src)

		files, sum, descs := PortableFiles(t, src)

		env.seeded = &devExport{files: files, descs: descs, sum: sum}
	})

	if env.seeded == nil {
		t.Fatal("the seeded export is missing: the case that built it failed")
	}

	return env.seeded
}

// with returns a copy of e with the rows added to table.
func (e *devExport) with(t *testing.T, table string, rows ...map[string]any) *devExport {
	t.Helper()

	files, descs, sum := AddPortableRows(t, e.files, e.descs, e.sum, table, rows...)

	return &devExport{files: files, descs: descs, sum: sum}
}

// stage stages e into dst with opts.
func (e *devExport) stage(t *testing.T, dst DevDataStore, opts backup.StageOptions) (backup.Summary, error) {
	t.Helper()

	return dst.StagePortable(t.Context(), PortableOpener(e.files), e.sum, e.descs, opts)
}

// mustStage stages e into dst as a backup of a production installation and
// fails on error.
func (e *devExport) mustStage(t *testing.T, dst DevDataStore) {
	t.Helper()

	opts := backup.StageOptions{BackupKind: installation.KindProduction, AllowDev: false, RestoreAt: restoreAt()}
	if _, err := e.stage(t, dst, opts); err != nil {
		t.Fatalf("stage the backup: %v", err)
	}
}

// keeperRows adds the rows of a person who is not a dev person, with a grant,
// a session and a token. The cases check that the strip leaves them.
func (e *devExport) withKeeper(t *testing.T) *devExport {
	t.Helper()

	out := e.with(t, "principals", DevPrincipalRow("prn-keeper", "alice"))
	out = out.with(t, "grants", DevGrantRow("grt-keeper", "prn-keeper"))
	out = out.with(t, "sessions", DevSessionRow("ses-keeper", "prn-keeper", "google"))

	return out.with(t, "access_tokens", DevTokenRow("tok-keeper", "prn-keeper"))
}

// tableRows exports s and returns the rows of table.
func tableRows(t *testing.T, s PortableStore, table string) []map[string]any {
	t.Helper()

	files, _, _ := PortableFiles(t, s)

	return readPortableLines(t, table, files[backup.PortablePath(table)])
}

// tablesOf exports s once and returns a reader of the rows of any table in that
// one export. A case that reads several tables of one state calls it once
// instead of exporting the store for each table.
func tablesOf(t *testing.T, s PortableStore) func(table string) []map[string]any {
	t.Helper()

	files, _, _ := PortableFiles(t, s)

	return func(table string) []map[string]any {
		t.Helper()

		return readPortableLines(t, table, files[backup.PortablePath(table)])
	}
}

// column returns the string values of one column of rows, sorted.
func column(rows []map[string]any, name string) []string {
	out := make([]string, 0, len(rows))

	for _, row := range rows {
		if value, ok := row[name].(string); ok {
			out = append(out, value)
		}
	}

	slices.Sort(out)

	return out
}

func testDevReadKind(t *testing.T, env *devEnv) {
	t.Helper()

	for _, kind := range []installation.Kind{installation.KindDev, installation.KindProduction} {
		s := env.newStore(t, kind)

		got, err := s.ReadKind(t.Context())
		if err != nil || got != kind {
			t.Fatalf("read kind of a %s database = %q, %v, want %q", kind, got, err, kind)
		}
	}

	// A database with no claimed kind: ReadKind reports ErrUnavailable and
	// claims nothing, so a later claim still stores its kind.
	s := env.newStore(t, "")

	for range 2 {
		if _, err := s.ReadKind(t.Context()); !errors.Is(err, installation.ErrUnavailable) {
			t.Fatalf("read kind of a database with no kind: err = %v, want ErrUnavailable", err)
		}
	}

	if got, err := s.ClaimKind(t.Context(), installation.KindDev); err != nil || got != installation.KindDev {
		t.Fatalf("claim after the reads = %q, %v, want dev", got, err)
	}

	closed := env.newStore(t, installation.KindProduction)
	closeStore(t, closed)

	_, err := closed.ReadKind(t.Context())
	if !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("read kind of a closed store: err = %v, want ErrUnavailable", err)
	}

	for _, text := range []string{"sql: database is closed", "closed pool", "SQLITE", "sqlite3", "pgx"} {
		if strings.Contains(err.Error(), text) {
			t.Fatalf("read kind error %q holds driver text %q", err, text)
		}
	}
}

// testDevStageKind calls the stage call directly, with the service check
// skipped: the stage compares the claimed kind of the backup with the live
// kind that it reads from its own database (layer 1c).
//
//nolint:exhaustruct_v5 // Each case names only the fields that it tests.
func testDevStageKind(t *testing.T, env *devEnv) {
	t.Helper()

	export := env.export(t)

	cases := []struct {
		name  string
		live  installation.Kind
		opts  backup.StageOptions
		want  error
		stage bool
	}{
		{
			name: "dev backup into production, flag off", live: installation.KindProduction,
			opts: backup.StageOptions{BackupKind: installation.KindDev, AllowDev: false, RestoreAt: restoreAt()},
			want: backup.ErrDevBackupRefused,
		},
		{
			name: "dev backup into production, flag on",
			live: installation.KindProduction,
			opts: backup.StageOptions{
				BackupKind: installation.KindDev,
				AllowDev:   true,
				RestoreAt:  restoreAt(),
			},
			stage: true,
		},
		{
			name: "production backup into production",
			live: installation.KindProduction,
			opts: backup.StageOptions{
				BackupKind: installation.KindProduction,
				AllowDev:   false,
				RestoreAt:  restoreAt(),
			},
			stage: true,
		},
		{
			name: "dev backup into dev",
			live: installation.KindDev,
			opts: backup.StageOptions{
				BackupKind: installation.KindDev,
				AllowDev:   false,
				RestoreAt:  restoreAt(),
			},
			stage: true,
		},
		{
			name: "production backup into dev",
			live: installation.KindDev,
			opts: backup.StageOptions{
				BackupKind: installation.KindProduction,
				AllowDev:   false,
				RestoreAt:  restoreAt(),
			},
			stage: true,
		},
		{
			name: "empty backup kind",
			live: installation.KindProduction,
			opts: backup.StageOptions{
				BackupKind: "",
				AllowDev:   true,
				RestoreAt:  restoreAt(),
			},
			want: backup.ErrManifestKindInvalid,
		},
		{
			name: "unknown backup kind",
			live: installation.KindProduction,
			opts: backup.StageOptions{
				BackupKind: "staging",
				AllowDev:   true,
				RestoreAt:  restoreAt(),
			},
			want: backup.ErrManifestKindInvalid,
		},
		{
			name: "upper case backup kind",
			live: installation.KindDev,
			opts: backup.StageOptions{
				BackupKind: "DEV",
				AllowDev:   true,
				RestoreAt:  restoreAt(),
			},
			want: backup.ErrManifestKindInvalid,
		},
		{
			name: "live kind not claimed", live: "",
			opts: backup.StageOptions{BackupKind: installation.KindProduction, AllowDev: true, RestoreAt: restoreAt()},
			want: installation.ErrUnavailable,
		},
	}

	for _, tc := range cases {
		dst := env.newStore(t, tc.live)

		_, err := export.stage(t, dst, tc.opts)

		switch {
		case tc.stage && err != nil:
			t.Fatalf("%s: stage = %v, want success", tc.name, err)
		case !tc.stage && !errors.Is(err, tc.want):
			t.Fatalf("%s: stage err = %v, want %v", tc.name, err, tc.want)
		default:
		}

		if tc.stage {
			continue
		}

		// A refused stage leaves no staging area and no row in the live data.
		if applyErr := dst.ApplyStaging(t.Context()); !errors.Is(applyErr, backup.ErrCorrupt) {
			t.Fatalf("%s: apply after a refused stage = %v, want ErrCorrupt (no staging area)", tc.name, applyErr)
		}

		if left := mustListNamespaces(t, dst); len(left) != 0 {
			t.Fatalf("%s: namespaces after a refused stage = %+v, want none", tc.name, left)
		}
	}
}

// devStripCase is one case of the strip: the rows it adds, and what the strip
// must report.
type devStripCase struct {
	name string
	add  func(t *testing.T, e *devExport) *devExport
	// classes is the want count of each class of the report. A class that is
	// not in it must count zero.
	classes map[backup.DevRemovalClass]int64
	// principals is the want subjects of the first principals of the report.
	principals map[string]map[backup.DevRemovalClass]int64
	// approvers is the want objects of the approver list of the report.
	approvers []backup.DevApproverRemoval
	// dev is false when the report must hold no dev data.
	noDev bool
	// bodies is the want body of the gate objects after the strip, by ID.
	bodies map[string]approverWant
	// after, when set, checks the live store after the swap.
	after func(t *testing.T, dst DevDataStore)
}

// approverWant is the want approvers of a gate object after the strip.
type approverWant struct {
	users  []string
	groups []string
	owners bool
}

const (
	bodyRule     = `{"approvers":{"groups":%s,"users":%s,"unitOwners":%t},"count":1,"authorMayApprove":false}`
	kindRule     = "approval-rule"
	kindDefault  = "approval-default"
	kindOwnerRle = "ownership-rule"
)

func ruleBody(groups, users string, owners bool) string {
	return fmt.Sprintf(bodyRule, groups, users, owners)
}

//nolint:exhaustruct_v5 // Each case names only the fields that it tests.
func devStripCases() []devStripCase {
	return []devStripCase{
		{
			name: "a dev principal with a grant, a session, a token, preferences, a group member, a local user, a session generation",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))
				out = out.with(t, "grants", DevGrantRow("grt-dev", "prn-dev"))
				out = out.with(t, "sessions", DevSessionRow("ses-dev", "prn-dev", "dev"))
				out = out.with(t, "access_tokens", DevTokenRow("tok-dev", "prn-dev"))
				out = out.with(t, "person_preferences", DevPreferencesRow("prn-dev"))
				out = out.with(t, "forge_account_links", DevForgeLinkRow("prn-dev"))
				out = out.with(t, "forge_link_requests", DevForgeLinkRequestRow("prn-dev"))
				out = out.with(t, "local_users", DevLocalUserRow("lus-dev", "prn-dev"))
				out = out.with(t, "principal_session_generations", DevSessionGenerationRow("prn-dev"))
				out = out.with(t, "passkeys", DevPasskeyRow("pky-dev", "prn-dev"))
				out = out.with(t, "principal_groups", DevGroupRow("grp-1"))

				return out.with(t, "group_members", DevMemberRow("grp-1", "prn-dev"))
			},
			classes: map[backup.DevRemovalClass]int64{
				backup.RemovedPrincipals: 1, backup.RemovedGrants: 1, backup.RemovedSessions: 1,
				backup.RemovedAccessTokens: 1, backup.RemovedGroupMembers: 1, backup.RemovedPreferences: 1,
				backup.RemovedForgeLinks: 1, backup.RemovedForgeLinkRequests: 1,
				backup.RemovedLocalUsers: 1, backup.RemovedSessionGenerations: 1, backup.RemovedPasskeys: 1,
			},
			principals: map[string]map[backup.DevRemovalClass]int64{
				"dev:admin": {
					backup.RemovedGrants: 1, backup.RemovedSessions: 1, backup.RemovedAccessTokens: 1,
					backup.RemovedGroupMembers: 1, backup.RemovedPreferences: 1, backup.RemovedForgeLinks: 1,
					backup.RemovedForgeLinkRequests: 1, backup.RemovedLocalUsers: 1,
					backup.RemovedSessionGenerations: 1, backup.RemovedPasskeys: 1,
				},
			},
		},
		{
			name: "the prefix in other ASCII letter cases",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.with(t, "principals",
					DevPrincipalRow("prn-a", "DEV:"), DevPrincipalRow("prn-b", "Dev:Admin"),
					DevPrincipalRow("prn-c", "dEv:x"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 3},
		},
		{
			name: "two subjects that differ only in case",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.with(t, "principals", DevPrincipalRow("prn-a", "dev:x"), DevPrincipalRow("prn-b", "DEV:x"))

				return out.with(t, "grants", DevGrantRow("grt-a", "prn-a"), DevGrantRow("grt-b", "prn-b"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 2, backup.RemovedGrants: 2},
			principals: map[string]map[backup.DevRemovalClass]int64{
				"dev:x": {backup.RemovedGrants: 1}, "DEV:x": {backup.RemovedGrants: 1},
			},
		},
		{
			name: "a provider with the reserved ID, its issuer record and its sign-in requests",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.with(t, "sign_in_providers", DevProviderRow("dev"), DevProviderRow("google"))
				out = out.with(t, "oidc_providers", DevIssuerRow("dev"), DevIssuerRow("DEV"), DevIssuerRow("google"))

				return out.with(t, "sign_in_requests",
					DevSignInRequestRow("req-1", "dev"), DevSignInRequestRow("req-2", "Dev"),
					DevSignInRequestRow("req-3", "google"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedProviders: 3, backup.RemovedSignInRequests: 2},
		},
		{
			name: "a session of the reserved provider for a person who is not a dev person",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.withKeeper(t).with(t, "sessions", DevSessionRow("ses-forged", "prn-keeper", "dev"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedSessions: 1},
		},
		{
			name: "sessions of a principal that is removed in part: one of the reserved provider, one of another",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.withKeeper(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))

				return out.with(t, "sessions",
					DevSessionRow("ses-dev-a", "prn-dev", "dev"), DevSessionRow("ses-dev-b", "prn-dev", "google"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 1, backup.RemovedSessions: 2},
		},
		{
			name: "temp admins by principal ID and by subject in another case",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.withKeeper(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))

				return out.with(t, "temp_admins",
					DevTempAdminRow("prn-dev", "dev:admin"), DevTempAdminRow("prn-keeper", "Dev:x"),
					DevTempAdminRow("prn-other", "recovery"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 1, backup.RemovedTempAdmins: 2},
			principals: map[string]map[backup.DevRemovalClass]int64{
				"dev:admin": {backup.RemovedTempAdmins: 1},
			},
		},
		{
			name: "a grant that names a missing principal goes with dev data",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.withKeeper(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))

				return out.with(t, "grants", DevGrantRow("grt-missing", "prn-missing"))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 1, backup.RemovedDangling: 1},
		},
		{
			name: "a grant that names a missing principal alone is not dev data",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.withKeeper(t).with(t, "grants", DevGrantRow("grt-missing", "prn-missing"))
			},
			noDev: true,
		},
		{
			name: "a dev subject in the approvers of an approval rule",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.with(t, "gate_objects", DevGateObjectRow(kindRule, "rule-a",
					ruleBody(`[]`, `["dev:admin","alice"]`, false)))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedApprovers: 1},
			approvers: []backup.DevApproverRemoval{
				{Kind: kindRule, Scope: "installation", ID: "rule-a", Replaced: false},
			},
			bodies: map[string]approverWant{"rule-a": {users: []string{"alice"}, groups: []string{}}},
		},
		{
			name: "a dev subject in an approval default that has other approvers",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.with(t, "gate_objects", DevGateObjectRow(kindDefault, "",
					ruleBody(`["ops"]`, `["DEV:x"]`, false)))
			},
			classes:   map[backup.DevRemovalClass]int64{backup.RemovedApprovers: 1},
			approvers: []backup.DevApproverRemoval{{Kind: kindDefault, Scope: "installation", ID: "", Replaced: false}},
			bodies:    map[string]approverWant{"": {users: []string{}, groups: []string{"ops"}}},
		},
		{
			name: "a dev subject as the only approver of an approval default",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.with(t, "gate_objects", DevGateObjectRow(kindDefault, "",
					ruleBody(`[]`, `["Dev:Y"]`, false)))
			},
			classes:   map[backup.DevRemovalClass]int64{backup.RemovedApprovers: 1},
			approvers: []backup.DevApproverRemoval{{Kind: kindDefault, Scope: "installation", ID: "", Replaced: true}},
			bodies:    map[string]approverWant{"": {users: []string{"!no-approver"}, groups: []string{}}},
		},
		{
			name: "a dev subject as the only approver of an ownership rule",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.with(t, "gate_objects", DevGateObjectRow(kindOwnerRle, "own-a",
					ruleBody(`[]`, `["dev:a","DEV:b"]`, false)))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedApprovers: 2},
			approvers: []backup.DevApproverRemoval{
				{Kind: kindOwnerRle, Scope: "installation", ID: "own-a", Replaced: true},
			},
			bodies: map[string]approverWant{"own-a": {users: []string{"!no-approver"}, groups: []string{}}},
		},
		{
			name: "a grant whose text subject names a dev principal and whose ID names another",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				grant := DevGrantRow("grt-split", "prn-keeper")
				grant["subject"] = "principal:prn-dev"

				out := e.withKeeper(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))

				return out.with(t, "grants", grant)
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 1, backup.RemovedGrants: 1},
		},
		{
			name: "a dev subject with a group and the unit owners: both stay",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				return e.with(t, "gate_objects", DevGateObjectRow(kindRule, "rule-b",
					ruleBody(`["ops"]`, `["dev:z"]`, true)))
			},
			classes: map[backup.DevRemovalClass]int64{backup.RemovedApprovers: 1},
			approvers: []backup.DevApproverRemoval{
				{Kind: kindRule, Scope: "installation", ID: "rule-b", Replaced: false},
			},
			bodies: map[string]approverWant{"rule-b": {users: []string{}, groups: []string{"ops"}, owners: true}},
		},
		{
			name: "no dev data",
			add: func(t *testing.T, e *devExport) *devExport {
				t.Helper()

				out := e.withKeeper(t).with(t, "sign_in_providers", DevProviderRow("google"))

				return out.with(t, "gate_objects", DevGateObjectRow(kindRule, "rule-c",
					ruleBody(`["ops"]`, `["alice"]`, false)))
			},
			noDev:  true,
			bodies: map[string]approverWant{"rule-c": {users: []string{"alice"}, groups: []string{"ops"}}},
		},
		devJobCase(),
	}
}

func testDevStrip(t *testing.T, env *devEnv) {
	t.Helper()

	base := env.export(t)

	// Each case has its own target store and reads base only, so the cases
	// run at the same time.
	for _, tc := range devStripCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkDevStrip(t, env, base, tc)
		})
	}
}

// checkDevStrip stages the export of tc into a fresh production store, strips
// it in both modes, swaps it in and checks the rows that stay.
func checkDevStrip(t *testing.T, env *devEnv, base *devExport, tc devStripCase) {
	t.Helper()

	export := tc.add(t, base)
	dst := env.newStore(t, installation.KindProduction)

	export.mustStage(t, dst)

	detected, err := dst.StripDevStaged(t.Context(), backup.DevStripDetect)
	if err != nil {
		t.Fatalf("%s: detect: %v", tc.name, err)
	}

	assertDevReport(t, tc, detected)

	removed, err := dst.StripDevStaged(t.Context(), backup.DevStripRemove)
	if err != nil {
		t.Fatalf("%s: remove: %v", tc.name, err)
	}

	assertDevReport(t, tc, removed)

	// After the removal the staged data holds no dev row.
	again, err := dst.StripDevStaged(t.Context(), backup.DevStripDetect)
	if err != nil {
		t.Fatalf("%s: detect after the removal: %v", tc.name, err)
	}

	if again.HasDev() {
		t.Fatalf("%s: dev data after the removal: %+v", tc.name, again)
	}

	// The swap takes the cleaned data, and the rows of other people stay.
	if applyErr := dst.ApplyStaging(t.Context()); applyErr != nil {
		t.Fatalf("%s: apply after the strip: %v", tc.name, applyErr)
	}

	assertAfterStrip(t, tc, dst)
}

// assertDevReport compares a report with the want of the case.
func assertDevReport(t *testing.T, tc devStripCase, got backup.DevStripReport) {
	t.Helper()

	if got.HasDev() == tc.noDev {
		t.Fatalf("%s: HasDev = %v, want %v (report %+v)", tc.name, got.HasDev(), !tc.noDev, got)
	}

	assertClasses(t, tc, got)
	assertPrincipalCounts(t, tc, got)

	if !slices.Equal(got.Approvers, tc.approvers) && (len(got.Approvers) > 0 || len(tc.approvers) > 0) {
		t.Fatalf("%s: approvers = %+v, want %+v", tc.name, got.Approvers, tc.approvers)
	}
}

// assertClasses compares the counts of the classes of a report with the case.
func assertClasses(t *testing.T, tc devStripCase, got backup.DevStripReport) {
	t.Helper()

	for class, want := range tc.classes {
		if got.Classes[class] != want {
			t.Fatalf("%s: class %s = %d, want %d (report %+v)", tc.name, class, got.Classes[class], want, got.Classes)
		}
	}

	for class, n := range got.Classes {
		if _, listed := tc.classes[class]; !listed && n != 0 {
			t.Fatalf("%s: class %s = %d, want 0", tc.name, class, n)
		}
	}
}

// assertPrincipalCounts compares the counts of the listed principals of a
// report with the case.
func assertPrincipalCounts(t *testing.T, tc devStripCase, got backup.DevStripReport) {
	t.Helper()

	for _, p := range got.Principals {
		want, listed := tc.principals[p.Subject]
		if !listed {
			continue
		}

		for class, n := range want {
			if p.Counts[class] != n {
				t.Fatalf("%s: %s %s = %d, want %d", tc.name, p.Subject, class, p.Counts[class], n)
			}
		}
	}
}

// assertAfterStrip checks the live data after the swap of a stripped backup.
func assertAfterStrip(t *testing.T, tc devStripCase, dst DevDataStore) {
	t.Helper()

	rowsOf := tablesOf(t, dst)

	for _, row := range rowsOf("principals") {
		subject := textOf(row, "subject")
		if len(subject) >= 4 && strings.EqualFold(subject[:4], "dev:") {
			t.Fatalf("%s: principal %q is live after the strip", tc.name, subject)
		}
	}

	for _, id := range column(rowsOf("sign_in_providers"), "provider_id") {
		if strings.EqualFold(id, "dev") {
			t.Fatalf("%s: provider %q is live after the strip", tc.name, id)
		}
	}

	if slices.Contains(column(rowsOf("principals"), "id"), "prn-keeper") {
		for table, id := range map[string]string{
			"principals": "prn-keeper", "grants": "grt-keeper", "access_tokens": "tok-keeper",
		} {
			if !slices.Contains(column(rowsOf(table), "id"), id) {
				t.Fatalf("%s: row %s of the other person is gone from %s", tc.name, id, table)
			}
		}
	}

	checkAfterBodies(t, tc, rowsOf("gate_objects"))

	if tc.after != nil {
		tc.after(t, dst)
	}
}

// approversOf is the approvers member of a stored body.
type approversOf struct {
	Groups     []string `json:"groups"`
	Users      []string `json:"users"`
	UnitOwners bool     `json:"unitOwners"`
}

// approverDoc is the part of a stored body that the cases read.
type approverDoc struct {
	Approvers approversOf `json:"approvers"`
	Count     int         `json:"count"`
}

// textOf returns the string value of key in row, or empty when it is no string.
func textOf(row map[string]any, key string) string {
	text, ok := row[key].(string)
	if !ok {
		return ""
	}

	return text
}

// checkAfterBodies checks the approvers of the gate objects after the strip.
func checkAfterBodies(t *testing.T, tc devStripCase, rows []map[string]any) {
	t.Helper()

	for id, want := range tc.bodies {
		var body string

		for _, row := range rows {
			if textOf(row, "id") == id {
				body = textOf(row, "body")
			}
		}

		var doc approverDoc

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("%s: body of %q = %q: %v", tc.name, id, body, err)
		}

		if !slices.Equal(doc.Approvers.Users, want.users) || !slices.Equal(doc.Approvers.Groups, want.groups) ||
			doc.Approvers.UnitOwners != want.owners || doc.Count != 1 {
			t.Fatalf("%s: body of %q = %s, want users %v groups %v owners %v",
				tc.name, id, body, want.users, want.groups, want.owners)
		}
	}
}

// testDevStripMatchesSharedTable stages one principal for each row of the
// shared table of subjects and expects the strip to remove exactly the dev
// subjects. The matcher of principal.IsDevSubject uses the same table.
func testDevStripMatchesSharedTable(t *testing.T, env *devEnv) {
	t.Helper()

	export := env.export(t)
	cases := principaltest.DevSubjectCases()
	rows := make([]map[string]any, 0, len(cases))

	var wantDev int64

	wantKept := make([]string, 0, len(cases))
	tableSubjects := make([]string, 0, len(cases))

	for i, tc := range cases {
		tableSubjects = append(tableSubjects, tc.Subject)
		rows = append(rows, DevPrincipalRow(fmt.Sprintf("prn-%03d", i), tc.Subject))

		if tc.Dev {
			wantDev++
		} else {
			wantKept = append(wantKept, tc.Subject)
		}
	}

	dst := env.newStore(t, installation.KindProduction)
	export.with(t, "principals", rows...).mustStage(t, dst)

	report, err := dst.StripDevStaged(t.Context(), backup.DevStripRemove)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	if got := report.Classes[backup.RemovedPrincipals]; got != wantDev {
		t.Fatalf("removed principals = %d, want %d (the shared table)", got, wantDev)
	}

	if applyErr := dst.ApplyStaging(t.Context()); applyErr != nil {
		t.Fatalf("apply after the strip: %v", applyErr)
	}

	// The seeded store holds principals of its own. Compare the subjects of the table only.
	var got []string

	for _, subject := range column(tableRows(t, dst, "principals"), "subject") {
		if slices.Contains(tableSubjects, subject) {
			got = append(got, subject)
		}
	}

	slices.Sort(wantKept)

	if !slices.Equal(got, wantKept) {
		t.Fatalf("live subjects after the strip = %q, want %q", got, wantKept)
	}
}

// testDevStripBounds checks that the report holds at most MaxAuditedPrincipals
// principals and sums the rest, and that a hostile subject comes out clean.
func testDevStripBounds(t *testing.T, env *devEnv) {
	t.Helper()

	export := env.export(t)

	const total = backup.MaxAuditedPrincipals + 150

	principals := make([]map[string]any, 0, total)
	grants := make([]map[string]any, 0, total)

	for i := range total {
		id := fmt.Sprintf("prn-%04d", i)
		principals = append(principals, DevPrincipalRow(id, fmt.Sprintf("dev:user-%04d", i)))
		grants = append(grants, DevGrantRow(fmt.Sprintf("grt-%04d", i), id))
	}

	// PostgreSQL text holds no NUL, so the NUL case lives in the unit test of the cleaner.
	hostile := "dev:evil\r\nforged\x1b[31m\x07" + strings.Repeat("x", 400)
	principals = append(principals, DevPrincipalRow("prn-hostile", hostile))

	dst := env.newStore(t, installation.KindProduction)
	staged := export.with(t, "principals", principals...).with(t, "grants", grants...)
	staged.mustStage(t, dst)

	report, err := dst.StripDevStaged(t.Context(), backup.DevStripDetect)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	if len(report.Principals) != backup.MaxAuditedPrincipals {
		t.Fatalf("report holds %d principals, want %d", len(report.Principals), backup.MaxAuditedPrincipals)
	}

	if want := int64(total + 1 - backup.MaxAuditedPrincipals); report.OtherPrincipals != want {
		t.Fatalf("other principals = %d, want %d", report.OtherPrincipals, want)
	}

	if got := report.Classes[backup.RemovedGrants]; got != total {
		t.Fatalf("grants = %d, want %d", got, total)
	}

	var listed int64

	for _, p := range report.Principals {
		listed += p.Counts[backup.RemovedGrants]
	}

	if listed+report.OtherCounts[backup.RemovedGrants] != total {
		t.Fatalf("grants of the listed principals %d plus the others %d, want %d",
			listed, report.OtherCounts[backup.RemovedGrants], total)
	}

	assertCleanSubjects(t, report)
}

// assertCleanSubjects checks that every subject of the report is clean and at
// most MaxReportedSubjectBytes long, and that a cut subject carries the flag.
func assertCleanSubjects(t *testing.T, report backup.DevStripReport) {
	t.Helper()

	var sawCut bool

	for _, p := range report.Principals {
		if len(p.Subject) > backup.MaxReportedSubjectBytes {
			t.Fatalf(
				"subject of %d bytes in the report, want at most %d",
				len(p.Subject),
				backup.MaxReportedSubjectBytes,
			)
		}

		for _, r := range p.Subject {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("subject %q in the report holds control character %U", p.Subject, r)
			}
		}

		if p.SubjectCut {
			sawCut = true
		}
	}

	if !sawCut {
		t.Fatal("the report marks no cut subject, want the hostile subject marked")
	}
}

// testDevStripModes checks that detect changes nothing, that two detects agree,
// and that a strip without a staging area reports ErrCorrupt.
func testDevStripModes(t *testing.T, env *devEnv) {
	t.Helper()

	dst := env.newStore(t, installation.KindProduction)

	for _, mode := range []backup.DevStripMode{backup.DevStripDetect, backup.DevStripRemove} {
		if _, err := dst.StripDevStaged(t.Context(), mode); !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("strip mode %d with no staging area: err = %v, want ErrCorrupt", mode, err)
		}
	}

	export := env.export(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))
	export.mustStage(t, dst)

	first, err := dst.StripDevStaged(t.Context(), backup.DevStripDetect)
	if err != nil {
		t.Fatalf("first detect: %v", err)
	}

	second, err := dst.StripDevStaged(t.Context(), backup.DevStripDetect)
	if err != nil {
		t.Fatalf("second detect: %v", err)
	}

	if !maps.Equal(first.Classes, second.Classes) || !first.HasDev() {
		t.Fatalf("detects disagree or found nothing: %+v and %+v", first, second)
	}

	// Detect removed nothing, so the swap check still sees the dev person.
	if applyErr := dst.ApplyStaging(t.Context()); !errors.Is(applyErr, backup.ErrDevBackupRefused) {
		t.Fatalf("apply after a detect: err = %v, want ErrDevBackupRefused", applyErr)
	}
}

// devSwapCases lists staged data that holds dev data of each class.
func devSwapCases() []struct {
	name string
	add  func(t *testing.T, e *devExport) *devExport
} {
	return []struct {
		name string
		add  func(t *testing.T, e *devExport) *devExport
	}{
		{name: "a dev principal with a grant", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin")).
				with(t, "grants", DevGrantRow("grt-dev", "prn-dev"))
		}},
		{name: "a dev principal in another case", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(t, "principals", DevPrincipalRow("prn-dev", "DEV:Admin"))
		}},
		{name: "a temp admin by subject", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(t, "temp_admins", DevTempAdminRow("prn-x", "Dev:x"))
		}},
		{name: "a session of the reserved provider", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.withKeeper(t).with(t, "sessions", DevSessionRow("ses-dev", "prn-keeper", "dev"))
		}},
		{name: "a provider with the reserved ID", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(t, "sign_in_providers", DevProviderRow("dev"))
		}},
		{name: "a sign-in request of the reserved provider", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(t, "sign_in_requests", DevSignInRequestRow("req-1", "dev"))
		}},
		{name: "a grant whose text subject names a dev principal", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			grant := DevGrantRow("grt-split", "prn-keeper")
			grant["subject"] = "principal:prn-dev"

			return e.withKeeper(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin")).
				with(t, "grants", grant)
		}},
		{name: "a dev approver in an approval rule", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(
				t,
				"gate_objects",
				DevGateObjectRow(kindRule, "rule-a", ruleBody(`[]`, `["dev:admin"]`, false)),
			)
		}},
		{name: "a dev approver in an approval default", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(t, "gate_objects", DevGateObjectRow(kindDefault, "", ruleBody(`["ops"]`, `["DEV:x"]`, false)))
		}},
		{name: "a dev approver in an ownership rule", add: func(t *testing.T, e *devExport) *devExport {
			t.Helper()

			return e.with(
				t,
				"gate_objects",
				DevGateObjectRow(kindOwnerRle, "own-a", ruleBody(`[]`, `["dEv:y"]`, false)),
			)
		}},
	}
}

// hostileApproverBodies lists bodies of gate objects that the gate codec reads
// as a list with dev:admin, and that a reader of the exact key names would not
// see: another case, the long s and the Kelvin sign that encoding/json folds to
// s and k, and a key that occurs twice.
func hostileApproverBodies() map[string]string {
	return map[string]string{
		"a top-level key in another case":    `{"Approvers":{"users":["dev:admin"]},"count":1}`,
		"an upper case top-level key":        `{"APPROVERS":{"users":["dev:admin"]},"count":1}`,
		"a key for users in another case":    `{"approvers":{"Users":["dev:admin"]},"count":1}`,
		"a key for groups in another case":   `{"approvers":{"Groups":["ops"],"users":["dev:admin"]},"count":1}`,
		"a key for unit owners in lowercase": `{"approvers":{"unitowners":true,"users":["dev:admin"]},"count":1}`,
		"the long s in the key for users":    `{"approvers":{"users":["alice"],"u\u017fers":["dev:admin"]},"count":1}`,
		"the Kelvin sign in a key":           `{"approvers":{"users":["alice"],"\u212aey":1,"unitOwners":false,"unitOwner\u017f":true},"count":1}`,
		"users twice":                        `{"approvers":{"users":["alice"],"users":["dev:admin"]},"count":1}`,
		"users twice in other cases":         `{"approvers":{"users":["alice"],"USERS":["dev:admin"]},"count":1}`,
		"approvers twice":                    `{"approvers":{"users":["alice"]},"approvers":{"users":["dev:admin"]},"count":1}`,
		"approvers twice in other cases":     `{"approvers":{"users":["alice"]},"APPROVERS":{"users":["dev:admin"]},"count":1}`,
		"data after the object":              `{"approvers":{"users":["alice"]}} {"approvers":{"users":["dev:admin"]}}`,
		"a body that is no object":           `["dev:admin"]`,
	}
}

// testDevStripHostileBodies checks that the strip fails closed on a body that
// the gate codec would read differently from an exact read of the key names,
// and that the check of the swap refuses it on its own.
func testDevStripHostileBodies(t *testing.T, env *devEnv) {
	t.Helper()

	base := env.export(t)

	for name, body := range hostileApproverBodies() {
		for _, kind := range []string{kindRule, kindDefault, kindOwnerRle} {
			label := name + " (" + kind + ")"

			// A subtest closes its stores when it ends, and -parallel caps the
			// subtests that run at once, so the number of cases does not decide
			// how many connections the suite holds. The cases share base only
			// to read it: with returns a copy.
			t.Run(label, func(t *testing.T) {
				t.Parallel()

				checkHostileApproversBody(
					t,
					env,
					base.with(t, "gate_objects", DevGateObjectRow(kind, "hostile", body)),
					label,
				)
			})
		}
	}
}

// checkHostileApproversBody expects the strip to fail closed in both modes and
// both swap paths to refuse the staged export, which holds one hostile
// gate object.
func checkHostileApproversBody(t *testing.T, env *devEnv, export *devExport, label string) {
	t.Helper()

	for _, mode := range []backup.DevStripMode{backup.DevStripDetect, backup.DevStripRemove} {
		dst := env.newStore(t, installation.KindProduction)
		export.mustStage(t, dst)

		if _, err := dst.StripDevStaged(t.Context(), mode); !errors.Is(err, backup.ErrDevStripIncomplete) {
			t.Fatalf("%s: strip mode %d: err = %v, want ErrDevStripIncomplete", label, mode, err)
		}
	}

	for swapKind, swap := range map[string]func(DevDataStore) error{
		"plain": func(dst DevDataStore) error { return applyPlain(t, dst) },
		"keep":  func(dst DevDataStore) error { return applyKeeping(t, dst) },
	} {
		checkSwapRefused(t, label+" ("+swapKind+")", env.newStore(t, installation.KindProduction), export, swap)
	}
}

// testDevSwapRefuses stages dev data into a production store with no strip
// run, and expects both swap paths to refuse and to leave the live data as it
// was (layer 3a).
func testDevSwapRefuses(t *testing.T, env *devEnv) {
	t.Helper()

	base := env.export(t)

	swaps := map[string]func(*testing.T, DevDataStore) error{
		"plain": applyPlain,
		"keep":  applyKeeping,
	}

	// Each case has its own target store and reads base only, so the cases
	// run at the same time.
	for _, tc := range devSwapCases() {
		for kind, swap := range swaps {
			name := tc.name + " (" + kind + ")"

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dst := env.newStore(t, installation.KindProduction)
				checkSwapRefused(t, name, dst, tc.add(t, base), func(dst DevDataStore) error { return swap(t, dst) })
			})
		}
	}
}

// applyPlain runs the plain swap and returns its error as it is.
func applyPlain(t *testing.T, dst DevDataStore) error {
	t.Helper()

	return dst.ApplyStaging(t.Context())
}

// applyKeeping runs the swap that keeps access and returns its error as it is.
func applyKeeping(t *testing.T, dst DevDataStore) error {
	t.Helper()

	_, err := dst.ApplyStagingKeepingAccess(t.Context())

	return err
}

// checkSwapRefused stages e into dst with no strip, and expects the swap to
// refuse with ErrDevBackupRefused and to leave the live data as it was.
func checkSwapRefused(t *testing.T, name string, dst DevDataStore, e *devExport, swap func(DevDataStore) error) {
	t.Helper()

	before := portableSummaryOf(t, dst)

	e.mustStage(t, dst)

	if err := swap(dst); !errors.Is(err, backup.ErrDevBackupRefused) {
		t.Fatalf("%s: swap err = %v, want ErrDevBackupRefused", name, err)
	}

	assertSameSummary(t, portableSummaryOf(t, dst), before)

	// The staging area is still there for the caller to discard.
	if again := dst.ApplyStaging(t.Context()); !errors.Is(again, backup.ErrDevBackupRefused) {
		t.Fatalf("%s: second swap err = %v, want ErrDevBackupRefused", name, again)
	}

	if discardErr := dst.DiscardStaging(t.Context()); discardErr != nil {
		t.Fatalf("%s: discard: %v", name, discardErr)
	}
}

// testDevSwapDevInstallation checks that a dev installation takes dev data.
func testDevSwapDevInstallation(t *testing.T, env *devEnv) {
	t.Helper()

	export := env.export(t).with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin"))
	dst := env.newStore(t, installation.KindDev)

	opts := backup.StageOptions{BackupKind: installation.KindDev, AllowDev: false, RestoreAt: restoreAt()}
	if _, err := export.stage(t, dst, opts); err != nil {
		t.Fatalf("stage into a dev installation: %v", err)
	}

	if err := dst.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply into a dev installation: %v", err)
	}

	if !slices.Contains(column(tableRows(t, dst, "principals"), "subject"), "dev:admin") {
		t.Fatal("the dev installation lost dev:admin in a restore of a dev backup")
	}
}

// testDevSwapUnreadableKind checks that a store whose live kind cannot be read
// refuses the swap.
func testDevSwapUnreadableKind(t *testing.T, env *devEnv) {
	t.Helper()

	dst := env.newStore(t, installation.KindProduction)

	eraser, ok := dst.(KindEraser)
	if !ok {
		t.Skip("the store cannot erase its kind behind the port")
	}

	env.export(t).mustStage(t, dst)

	if err := eraser.EraseKindForTest(t.Context()); err != nil {
		t.Fatalf("erase the kind: %v", err)
	}

	for _, keep := range []bool{false, true} {
		var err error

		if keep {
			_, err = dst.ApplyStagingKeepingAccess(t.Context())
		} else {
			err = dst.ApplyStaging(t.Context())
		}

		if !errors.Is(err, installation.ErrUnavailable) {
			t.Fatalf("swap with no readable kind (keep access %v): err = %v, want ErrUnavailable", keep, err)
		}
	}
}

// testDevKeepDropsTempAdmins checks the swap that keeps the access tables: it
// drops the staged temp admins that name a principal that the live principals
// do not hold, counts them, and keeps the rows of live principals. The strip
// removes the row with a dev subject.
func testDevKeepDropsTempAdmins(t *testing.T, env *devEnv) {
	t.Helper()

	dst := env.newStore(t, installation.KindProduction)

	// Give the live data a principal: a swap of a first backup brings it.
	// Both backups come from one export, so they name the same namespaces: the
	// kept installation rows of the live data name a namespace that the second
	// backup holds.
	base := env.export(t)
	first := base.with(t, "principals", DevPrincipalRow("prn-live", "alice"))
	first.mustStage(t, dst)

	if err := dst.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply the first backup: %v", err)
	}

	second := base.
		with(t, "principals", DevPrincipalRow("prn-live", "alice")).
		with(t, "temp_admins",
			DevTempAdminRow("prn-live", "alice"), DevTempAdminRow("prn-absent", "bob"),
			DevTempAdminRow("prn-live-2", "Dev:x"))
	second.mustStage(t, dst)

	report, err := dst.StripDevStaged(t.Context(), backup.DevStripRemove)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}

	// The strip is free of the path: it removes the row with the dev subject
	// and judges no other row against the staged principals.
	if got := report.Classes[backup.RemovedTempAdmins]; got != 1 {
		t.Fatalf("strip removed %d temp admins, want 1 (the dev subject)", got)
	}

	result, err := dst.ApplyStagingKeepingAccess(t.Context())
	if err != nil {
		t.Fatalf("apply keeping access: %v", err)
	}

	if result.DroppedTempAdmins != 1 {
		t.Fatalf("dropped temp admins = %d, want 1 (the row of prn-absent)", result.DroppedTempAdmins)
	}

	got := column(tableRows(t, dst, "temp_admins"), "principal_id")
	if !slices.Equal(got, []string{"prn-live"}) {
		t.Fatalf("temp admins after the swap = %q, want only prn-live", got)
	}
}

// testDevKeepWholeArea checks that the strip and the swap look at the staged
// access tables on the keep path too, although the swap does not take them.
func testDevKeepWholeArea(t *testing.T, env *devEnv) {
	t.Helper()

	dst := env.newStore(t, installation.KindProduction)
	env.export(t).
		with(t, "principals", DevPrincipalRow("prn-dev", "dev:admin")).
		mustStage(t, dst)

	detected, err := dst.StripDevStaged(t.Context(), backup.DevStripDetect)
	if err != nil || !detected.HasDev() {
		t.Fatalf("detect on the keep path = %+v, %v, want dev data", detected, err)
	}

	if _, err := dst.ApplyStagingKeepingAccess(t.Context()); !errors.Is(err, backup.ErrDevBackupRefused) {
		t.Fatalf("keep swap with a dev principal in the staged access tables: err = %v, want ErrDevBackupRefused", err)
	}

	if _, err := dst.StripDevStaged(t.Context(), backup.DevStripRemove); err != nil {
		t.Fatalf("strip: %v", err)
	}

	if _, err := dst.ApplyStagingKeepingAccess(t.Context()); err != nil {
		t.Fatalf("keep swap after the strip: %v", err)
	}
}

// testDevRecordReport checks the report in the restore record.
func testDevRecordReport(t *testing.T, env *devEnv) {
	t.Helper()

	dst := env.newStore(t, installation.KindProduction)
	ctx := t.Context()

	record := mustCreateRestore(
		t,
		dst,
		newRestoreRecord(backup.MintRestoreID(), "bkp-1", time.Unix(1_700_000_000, 0).UTC()),
	)

	if got := mustGetRestore(t, dst, record.ID); got.DevReport != nil {
		t.Fatalf("report of a new restore = %+v, want none", got.DevReport)
	}

	report := backup.DevStripReport{
		Principals: []backup.DevRemoval{{
			Subject: "dev:admin", SubjectCut: false,
			Counts: map[backup.DevRemovalClass]int64{backup.RemovedGrants: 2},
		}},
		OtherPrincipals: 3,
		OtherCounts:     map[backup.DevRemovalClass]int64{backup.RemovedSessions: 4},
		Classes:         map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 4, backup.RemovedGrants: 2},
		Approvers: []backup.DevApproverRemoval{
			{Kind: "approval-rule", Scope: "installation", ID: "rule-a", Replaced: true},
		},
		Warnings:       []backup.RestoreWarningCode{backup.WarnDevBackupIntoProduction},
		PreBackupFound: nil,
		NotHeld:        nil,
		ErrorCode:      "",
	}

	fence := restoreClaim(t, dst, record.ID).Step(backup.RestoreStepCheck)
	if err := restorePortsOf(t, dst).ops.RecordRestoreReport(ctx, fence, record.ID, report); err != nil {
		t.Fatalf("record the report: %v", err)
	}

	got := mustGetRestore(t, dst, record.ID)
	if got.DevReport == nil || !reportsEqual(*got.DevReport, report) {
		t.Fatalf("report of the restore = %+v, want %+v", got.DevReport, report)
	}

	listed, err := dst.ListRestores(ctx)
	if err != nil || len(listed) != 1 || listed[0].DevReport == nil {
		t.Fatalf("list restores = %+v, %v, want the row with its report", listed, err)
	}

	// The record is the place where the report outlives the swap.
	env.export(t).mustStage(t, dst)

	if applyErr := dst.ApplyStaging(ctx); applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}

	after := mustGetRestore(t, dst, record.ID)
	if after.DevReport == nil || !reportsEqual(*after.DevReport, report) {
		t.Fatalf("report of the restore after the swap = %+v, want %+v", after.DevReport, report)
	}
}

// reportsEqual compares two reports field by field.
func reportsEqual(a, b backup.DevStripReport) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)

	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}
