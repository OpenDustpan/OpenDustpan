//go:build e2e && upgrade

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
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
	// mixedCurrentTwice is the label of the peer that is a second process of
	// the current binary, for a repository before its first release tag.
	mixedCurrentTwice = "current-twice"
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

// mixedPeer is one peer binary of the matrix: the release tag it was built
// from, or the current binary when the repository has no release tag. The
// label names the peer in the output.
type mixedPeer struct {
	label string
	bin   string
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

// mixedReleaseTags returns the newest mixedReleases release tags that are
// ancestors of HEAD, in version order. The matrix ignores non-release tags
// and tags that are not ancestors of HEAD. A shallow clone that shows no
// tag fails with the fetch guidance: the empty list is an incomplete fetch,
// not a repository before its first tag.
func mixedReleaseTags(t *testing.T) []string {
	t.Helper()

	ctx := t.Context()
	all := e2e.ReleaseTags(ctx, t)

	ancestors := make(map[string]bool)

	for _, tag := range e2e.AncestorReleaseTags(ctx, t) {
		ancestors[tag] = true
	}

	tags := e2e.MixedTagChoice(all, mixedReleases, func(tag string) bool { return ancestors[tag] })

	if len(tags) == 0 {
		if err := e2e.MixedTagFetchError(e2e.RepositoryFetch(ctx, t)); err != nil {
			t.Fatal(err)
		}
	}

	return tags
}

// TestMixedVersion checks that the current binary and the binary of each of
// the last two release tags serve on one database. The current binary starts
// on a new database, the real Pulumi CLI and OpenTofu write state through
// it, and then a peer binary serves on the same database. The real CLIs read
// the state of the current binary and write through the peer, and the
// current binary reads what the peer wrote. A peer that the schema minimum
// refuses is allowed only when it prints the schema refusal. With no release
// tag the peer is a second process of the current binary, and the test logs
// that it ran over 0 tags, so it never skips silently. One PostgreSQL
// database also runs the reverse start order: the old binary first, the
// current binary second. Run it with `make mixed-version-test`; it is not
// part of `make check`.
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

	ctx := t.Context()
	tags := mixedReleaseTags(t)

	mixedReportTags(t, tags)

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tools := installUpgradeTools(t, fetcher)
	newBin := e2e.BuildCurrentBinary(ctx, t, t.TempDir())

	peers := mixedBuildPeers(ctx, t, tags, newBin)

	matrix := mixedMatrix{
		tags:      tags,
		peers:     peers,
		databases: databases,
		newBin:    newBin,
		tools:     tools,
	}

	mixedRunMatrix(t, matrix)
	mixedRunReverse(t, matrix)
}

// mixedReportTags logs the release tags that the matrix runs, and writes the
// warning annotation of a run without a release tag, so a workflow shows the
// run on one line and the step never skips silently.
func mixedReportTags(t *testing.T, tags []string) {
	t.Helper()

	t.Log(e2e.MixedTagReport(tags))

	if len(tags) > 0 {
		return
	}

	// A workflow command at the start of a line, so GitHub shows the warning on the run.
	annotation := "::warning title=Mixed-version matrix::" + e2e.MixedTagReport(nil)

	if _, err := fmt.Fprintln(os.Stdout, annotation); err != nil {
		t.Logf("write the warning annotation: %v", err)
	}
}

// mixedBuildPeers builds the peer binary of each release tag, or the current
// binary when the repository has no release tag. A tag that cannot run fails
// the run and names itself, and the other tags still build, so one working
// tag never hides a broken one and an incomplete fetch never falls back to
// the 0-tag report.
func mixedBuildPeers(ctx context.Context, t *testing.T, tags []string, newBin string) []mixedPeer {
	t.Helper()

	return mixedBuildTagPeers(ctx, t, tags, newBin, func(ctx context.Context, tag string) (string, error) {
		return mixedTagPeer(ctx, t, tag)
	})
}

// mixedTagPeer builds the peer of one release tag. A tag without the minimum
// binary version check gives no refusal signal, so it fails without a build
// attempt and the error names the tag.
func mixedTagPeer(ctx context.Context, t *testing.T, tag string) (string, error) {
	t.Helper()

	if !e2e.TagHasMinimumCheck(ctx, t, tag) {
		return "", e2e.MixedMinimumTagError(tag)
	}

	return e2e.MixedBuildTag(ctx, t, tag, t.TempDir())
}

// mixedBuildReporter reports what the peer build loop does: the failure of a
// tag that cannot run, and the log line of a tag that builds. A case can
// record the failures and check the loop without failing its own run.
type mixedBuildReporter interface {
	Helper()
	Error(args ...any)
	Logf(format string, args ...any)
}

// mixedBuildTagPeers builds one peer per tag with build. It reports the
// failure of each tag that cannot run and keeps building the other tags, so
// the run fails with every broken tag named and one working tag never hides a
// broken one. With no tag the peer is a second process of the current binary.
func mixedBuildTagPeers(ctx context.Context, rep mixedBuildReporter, tags []string, newBin string,
	build func(ctx context.Context, tag string) (string, error),
) []mixedPeer {
	rep.Helper()

	if len(tags) == 0 {
		return []mixedPeer{{label: mixedCurrentTwice, bin: newBin}}
	}

	peers := make([]mixedPeer, 0, len(tags))

	for _, tag := range tags {
		bin, err := build(ctx, tag)
		if err != nil {
			rep.Error(err)

			continue
		}

		rep.Logf("built the release tag %s at %s and the current binary at %s", tag, bin, newBin)

		peers = append(peers, mixedPeer{label: tag, bin: bin})
	}

	return peers
}

// mixedMatrix groups what one full matrix run needs: the release tags that
// run, the peer binaries to serve, the databases to run on, the current
// binary and the CLIs that drive both.
type mixedMatrix struct {
	tags      []string
	peers     []mixedPeer
	databases []e2e.Store
	newBin    string
	tools     upgradeTools
}

// mixedRunMatrix runs one mixed-version subtest for each peer and store.
func mixedRunMatrix(t *testing.T, in mixedMatrix) {
	t.Helper()

	for _, peer := range in.peers {
		for _, db := range in.databases {
			t.Run(peer.label+"/"+string(db), func(t *testing.T) {
				t.Parallel()
				mixedOnStore(t, mixedRun{
					label:   peer.label,
					peerBin: peer.bin,
					newBin:  in.newBin,
					tools:   in.tools,
					store:   db,
				})
			})
		}
	}
}

// mixedRunReverse starts the old binary before the current binary on one
// PostgreSQL database. The start order does not matter, so the reverse order
// runs too, when a release tag and the PostgreSQL store are in the matrix.
func mixedRunReverse(t *testing.T, in mixedMatrix) {
	t.Helper()

	if len(in.tags) == 0 || !slices.Contains(in.databases, e2e.StorePostgres) {
		t.Log("the reverse start order needs a release tag and the PostgreSQL store: " +
			"set DUSTPAN_TEST_POSTGRES_URL to run it")

		return
	}

	tag := in.tags[len(in.tags)-1]

	for _, peer := range in.peers {
		if peer.label != tag {
			continue
		}

		t.Run("reverse/"+tag+"/"+string(e2e.StorePostgres), func(t *testing.T) {
			t.Parallel()
			mixedReverseOnStore(t, mixedRun{
				label:   tag,
				peerBin: peer.bin,
				newBin:  in.newBin,
				tools:   in.tools,
				store:   e2e.StorePostgres,
			})
		})

		return
	}
}

// mixedOnStore writes state through the current binary, serves the peer on
// the same database, and checks the CLIs against both servers. SQLite serves
// one process at a time, so there the current binary stops before the peer
// takes the file.
func mixedOnStore(t *testing.T, in mixedRun) {
	t.Helper()

	if in.store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, in.tools.pulumi, in.store)
	wsID := writeUpgradeState(ctx, t, env, in.tools.tofu)

	t.Logf("serving %s next to the current binary on the same %s database", in.label, in.store)

	res := env.TryPeer(ctx, t, in.peerBin)
	if err := peerVerdict(in.store, in.label, res); err != nil {
		t.Fatal(err)
	}

	if in.store == e2e.StoreSQLite {
		t.Logf("the peer %s is refused while the current binary holds the SQLite file", in.label)
		mixedPeerAlone(ctx, t, env, in, wsID)

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

// mixedPeerAlone serves the peer alone on one SQLite file: the current
// binary stops, the peer takes the file, the real CLIs write through it, and
// the current binary starts again. Two servers never hold the file at the
// same time, because a SQLite file serves one process.
func mixedPeerAlone(ctx context.Context, t *testing.T, env *e2e.Env, in mixedRun, wsID string) {
	t.Helper()

	env.StopServer(ctx, t)

	env.SetServerBinary(in.peerBin)
	env.StartServer(ctx, t)

	t.Logf("serving the peer %s alone on the SQLite file", in.label)

	checkPeerServes(ctx, t, env, in.tools, wsID)

	env.StopServer(ctx, t)
	env.SetServerBinary(in.newBin)
	env.StartServer(ctx, t)

	checkUpgradeSurvived(ctx, t, env, in.tools, wsID)
}

// mixedReverseOnStore starts the old binary on a new PostgreSQL database,
// writes state through it, starts the current binary on the same database,
// and checks that the tool steps against the old binary still complete while
// the current binary serves.
func mixedReverseOnStore(t *testing.T, in mixedRun) {
	t.Helper()

	if e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithBinary(ctx, t, in.tools.pulumi, in.store, in.peerBin)
	wsID := writeUpgradeState(ctx, t, env, in.tools.tofu)

	t.Logf("the old binary %s started first on a new %s database", in.label, in.store)

	peer := env.StartPeer(ctx, t, in.newBin)

	t.Logf("the current binary started second on the same %s database", in.store)

	checkPeerServes(ctx, t, peer, in.tools, wsID)
	checkUpgradeSurvived(ctx, t, env, in.tools, wsID)

	env.SetCLI(in.tools.pulumi)
	env.Step(ctx, t, step("old binary up after the current binary started", nil,
		"up", "--yes", "--non-interactive"))

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
// process guard message of the store: a SQLite file serves one process, so
// the matrix serves the peer alone and no two servers share the file. On
// PostgreSQL the peer must start, unless the label has a reason in
// mixedAllowedRefusals and the peer printed the schema refusal. It returns
// nil when the result is allowed.
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
