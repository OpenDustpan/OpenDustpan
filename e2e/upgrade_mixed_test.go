//go:build e2e && upgrade

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// mixedReleases holds how many of the newest release tags the mixed-version
	// test runs next to the current binary. Binaries of the last two releases
	// and the current build must run together on one schema.
	mixedReleases = 2
	// mixedPeerStack is the stack that the peer binary creates, so the current
	// binary can read what the peer wrote.
	mixedPeerStack = "peer"
	// mixedMinimum is the minimum binary version that the self-check writes
	// into the schema of its database.
	mixedMinimum = "v1.0.0"
	// mixedBelow is a binary version below mixedMinimum.
	mixedBelow = "v0.9.0"
)

// sqliteGuardMessage is the text of the process guard of the SQLite store.
// A second process on one SQLite file must stop with it.
var errMixedPeer = errors.New("mixed-version peer result is not allowed")

const sqliteGuardMessage = "another process holds the SQLite database file"

// mixedAllowedRefusals names the peers that the schema may refuse on
// PostgreSQL, keyed by the label of the peer, with the reason. The rolling
// window runs the last releases together on one schema, so a refusal of such
// a peer fails the matrix unless it has a row here. The table is empty.
func mixedAllowedRefusals() map[string]string {
	return map[string]string{}
}

// lastMixedTags returns the newest mixedReleases tags of all, in version
// order. With fewer tags it returns every tag.
func lastMixedTags(all []string) []string {
	return all[max(0, len(all)-mixedReleases):]
}

// mixedRun groups what one mixed-version run on one store needs.
type mixedRun struct {
	// label names the peer in the output: a release tag, or current-twice.
	label   string
	peerBin string
	newBin  string
	tools   upgradeTools
	store   e2e.Store
}

// TestMixedVersion checks that the current binary and the binary of each of
// the last two release tags serve on one database at the same time. The
// current binary starts on a new database, the real Pulumi CLI and OpenTofu
// write state through it, and then the peer binary starts on the same
// database. The real CLIs read the state of the current binary and write
// through the peer, and the current binary reads what the peer wrote. A peer
// that the schema minimum refuses is allowed only when it prints the schema
// refusal. With no release tag the peer is a second process of the current
// binary, and the test logs that it ran over 0 tags, so it never skips
// silently. Run it with `make mixed-version-test`; it is not part of
// `make check`.
func TestMixedVersion(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux.
		t.Skipf("the mixed-version test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Fatal("the mixed-version test needs E2E_SERVER=binary (run make mixed-version-test)")
	}

	tags := lastMixedTags(listReleaseTags(t))

	t.Logf("mixed-version matrix ran over %d tags: %s", len(tags), strings.Join(tags, ", "))

	if len(tags) == 0 {
		t.Log("no v* tag is reachable from HEAD: the peer is a second process of the current binary")
		// A workflow command at the start of a line, so GitHub shows the warning on the run.
		const annotation = "::warning title=Mixed-version matrix::ran over 0 tags: " +
			"no v* tag is reachable from HEAD, so the peer is a second process of the current binary"

		if _, err := fmt.Fprintln(os.Stdout, annotation); err != nil {
			t.Logf("write the warning annotation: %v", err)
		}
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tools := installUpgradeTools(t, fetcher)
	newBin := e2e.BuildCurrentBinary(t.Context(), t, t.TempDir())
	peers := map[string]string{"current-twice": newBin}

	if len(tags) > 0 {
		delete(peers, "current-twice")
	}

	for _, tag := range tags {
		peers[tag] = e2e.BuildTagBinary(t.Context(), t, tag, t.TempDir())
		t.Logf("built tag %s at %s and the current binary at %s", tag, peers[tag], newBin)
	}

	for label, peerBin := range peers {
		for _, db := range databases {
			t.Run(label+"/"+string(db), func(t *testing.T) {
				t.Parallel()
				mixedOnStore(t, mixedRun{
					label:   label,
					peerBin: peerBin,
					newBin:  newBin,
					tools:   tools,
					store:   db,
				})
			})
		}
	}
}

// mixedOnStore writes state through the current binary, starts the peer on
// the same database, and checks the CLIs against both servers.
func mixedOnStore(t *testing.T, in mixedRun) {
	t.Helper()

	if in.store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, in.tools.pulumi, in.store)
	wsID := writeUpgradeState(ctx, t, env, in.tools.tofu)

	t.Logf("starting %s next to the current binary on the same %s database", in.label, in.store)

	res := env.TryPeer(ctx, t, in.peerBin)
	if err := peerVerdict(in.store, in.label, res); err != nil {
		t.Fatal(err)
	}

	if in.store == e2e.StoreSQLite {
		t.Logf("peer %s is refused on SQLite, as a SQLite file serves one process", in.label)
		checkUpgradeSurvived(ctx, t, env, in.tools, wsID)

		return
	}

	if res.ExitCode != 0 {
		t.Logf("peer %s is refused for a reason in mixedAllowedRefusals", in.label)
		checkUpgradeSurvived(ctx, t, env, in.tools, wsID)

		return
	}

	peer := env.StartPeer(ctx, t, in.peerBin)

	checkPeerServes(ctx, t, peer, in.tools, wsID)

	env.SetCLI(in.tools.pulumi)
	env.Step(ctx, t, step("current reads peer stack", contains(`"deployment"`),
		"stack", "export", "--stack", fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, mixedPeerStack)))
	checkUpgradeSurvived(ctx, t, env, in.tools, wsID)

	peer.StopServer(ctx, t)
	checkUpgradeSurvived(ctx, t, env, in.tools, wsID)
}

// checkPeerServes drives the real CLIs against the peer: it reads the state
// that the current binary wrote, checks the held lock, and writes a new
// Pulumi stack with a secret.
func checkPeerServes(ctx context.Context, t *testing.T, peer *e2e.Env, tools upgradeTools, wsID string) {
	t.Helper()

	peer.SetCLI(tools.pulumi)

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, mixedPeerStack)

	peer.Step(ctx, t, step("peer login", contains(peer.URL), "login"))
	peer.Step(ctx, t, step("peer stack init", nil, "stack", "init", qualified))
	peer.Step(ctx, t, step("peer config set secret", nil, "config", "set", "--secret", "apiKey", upgradeSecret))
	peer.Step(ctx, t, step("peer up", nil, "up", "--yes", "--non-interactive"))

	peer.SetCLI(tools.tofu)

	peer.Step(ctx, t, step("peer init", nil, "init", "-input=false", "-no-color"))
	peer.Step(ctx, t, step("peer state pull", contains(`"greeting"`), "state", "pull"))
	checkUpgradeLocked(ctx, t, peer, wsID)

	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	status, body, err := peer.TFEStatusB(ctx, http.MethodPost, lockPath, `{"data":{}}`)
	if err != nil || status != http.StatusConflict {
		t.Fatalf("a lock by another principal through the peer answers %d, want %d: %s (%v)",
			status, http.StatusConflict, body, err)
	}
}

// TestMixedVersionBelowMinimum is the self-check of the matrix: when the
// schema records a minimum binary version, a peer below it is refused with
// the schema refusal while the current binary keeps serving, and a peer at
// the minimum starts. It proves that the matrix can see a refusal.
func TestMixedVersionBelowMinimum(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the mixed-version test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Fatal("the mixed-version test needs E2E_SERVER=binary (run make mixed-version-test)")
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tools := installUpgradeTools(t, fetcher)
	below := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), mixedBelow)
	atMinimum := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), mixedMinimum)

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			ctx := t.Context()
			env := e2e.NewEnv(ctx, t, tools.pulumi, db)
			insertServerFuture(ctx, t, env, db, mixedMinimum)
			checkMinimumRefusal(t, env, db, below, atMinimum)
			checkVerdictRows(t, env, db, below)

			env.Step(ctx, t, step("login after refusal", contains(env.URL), "login"))
		})
	}
}

// checkMinimumRefusal starts a binary below the schema minimum and a binary
// at it. The first must exit with the schema refusal and the second must
// start. A SQLite file serves one process, so on SQLite the probe runs while
// the server is stopped.
func checkMinimumRefusal(t *testing.T, env *e2e.Env, db e2e.Store, below, atMinimum string) {
	t.Helper()

	ctx := t.Context()
	try := env.TryPeer
	if db == e2e.StoreSQLite {
		env.StopServer(ctx, t)

		try = env.TryServe

		defer env.StartServer(ctx, t)
	}

	res := try(ctx, t, below)
	if res.ExitCode == 0 || !e2e.HasSchemaRefusal(res.Stdout+res.Stderr) {
		t.Fatalf("a binary at %s below the minimum %s must exit non-zero with the schema refusal, got exit %d:\n%s%s",
			mixedBelow, mixedMinimum, res.ExitCode, res.Stdout, res.Stderr)
	}

	if res := try(ctx, t, atMinimum); res.ExitCode != 0 {
		t.Fatalf("a binary at the minimum %s must start, got exit %d:\n%s%s",
			mixedMinimum, res.ExitCode, res.Stdout, res.Stderr)
	}
}

// peerVerdict judges how a peer ended. On SQLite the peer must stop with the
// process guard message of the store. On PostgreSQL the peer must start,
// unless the label has a reason in mixedAllowedRefusals and the peer printed
// the schema refusal. It returns nil when the result is allowed.
func peerVerdict(store e2e.Store, label string, res e2e.BinaryResult) error {
	output := res.Stdout + res.Stderr

	if store == e2e.StoreSQLite {
		if res.ExitCode == 0 {
			return fmt.Errorf(
				"%w: peer %s started on a SQLite file that another process holds:\n%s",
				errMixedPeer,
				label,
				output,
			)
		}

		if !strings.Contains(output, sqliteGuardMessage) {
			return fmt.Errorf("%w: peer %s exits with %d without the message %q:\n%s",
				errMixedPeer, label, res.ExitCode, sqliteGuardMessage, output)
		}

		return nil
	}

	if res.ExitCode == 0 {
		return nil
	}

	reason, allowed := mixedAllowedRefusals()[label]
	if !allowed {
		return fmt.Errorf("%w: peer %s was refused with exit %d, but the rolling window must run together; "+
			"add a named exception to mixedAllowedRefusals only with a reason:\n%s", errMixedPeer, label, res.ExitCode, output)
	}

	if !e2e.HasSchemaRefusal(output) {
		return fmt.Errorf("%w: peer %s exits with %d (allowed: %s) but prints no schema refusal:\n%s",
			errMixedPeer, label, res.ExitCode, reason, output)
	}

	return nil
}

// checkVerdictRows proves that the matrix fails on the two cases it must
// catch. On PostgreSQL, a peer below the schema minimum is refused, and
// peerVerdict must reject that result because the label has no exception.
// On SQLite, /bin/false stands for any peer that exits non-zero without the
// process guard message, and peerVerdict must reject it.
func checkVerdictRows(t *testing.T, env *e2e.Env, db e2e.Store, below string) {
	t.Helper()

	ctx := t.Context()

	if db == e2e.StoreSQLite {
		res := env.TryPeer(ctx, t, "/bin/false")
		if err := peerVerdict(db, "bin-false", res); err == nil {
			t.Fatalf("the SQLite verdict accepts /bin/false (exit %d): any non-zero exit would pass", res.ExitCode)
		}

		return
	}

	res := env.TryPeer(ctx, t, below)
	if err := peerVerdict(db, mixedBelow, res); err == nil {
		t.Fatalf("the PostgreSQL verdict accepts the refusal of a peer at %s: a refused peer would pass", mixedBelow)
	}
}
