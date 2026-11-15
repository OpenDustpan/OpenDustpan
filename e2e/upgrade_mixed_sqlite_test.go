//go:build e2e && upgrade

package e2e_test

import (
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestMixedVersionSQLiteSequential proves the SQLite rule of the
// mixed-version matrix: one binary at a time on one file. A second binary on
// the file is refused by the process guard of the store, and each binary
// serves alone and completes the real Pulumi CLI and OpenTofu steps. The
// peer is the current build stamped at the minimum binary version, so the
// case needs no release tag. Run it with `make mixed-version-test`.
func TestMixedVersionSQLiteSequential(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux.
		t.Skipf("the mixed-version test needs Linux, not %s", runtime.GOOS)
	}

	ctx := t.Context()
	fetcher := sharedFetcher(t)

	tools := installUpgradeTools(t, fetcher)
	newBin := e2e.BuildCurrentBinary(ctx, t, t.TempDir())
	peerBin := e2e.BuildBinaryWithVersion(ctx, t, t.TempDir(), mixedMinimum)

	env := e2e.NewEnvWithBinary(ctx, t, tools.pulumi, e2e.StoreSQLite, newBin)
	wsID := writeUpgradeState(ctx, t, env, tools.tofu)

	// A second binary on the file is refused: two servers never hold one
	// SQLite file at the same time.
	res := env.TryPeer(ctx, t, peerBin)
	if err := peerVerdict(e2e.StoreSQLite, "peer-alone", res); err != nil {
		t.Fatal(err)
	}

	mixedPeerAlone(ctx, t, env, mixedRun{
		label:   "peer-alone",
		peerBin: peerBin,
		newBin:  newBin,
		tools:   tools,
		store:   e2e.StoreSQLite,
	}, wsID)
}
