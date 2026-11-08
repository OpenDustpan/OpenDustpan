//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Pair is two in-process servers for one test: a source S and a destination
// D. Each has its own database, blob directory, tokens and namespace. The
// key-encryption key is the fixed test value on both, but each server wraps
// its own data keys in its own database, so the stores stay separate.
//
// Two Env values, not one Env with a second server, because NewEnv already
// builds one isolated server and the pair reuses it twice. A second server
// inside Env would change Env, cliEnv and every existing scenario. The pair
// shares one Recorder, so one SetStep labels the requests to both servers
// and the merged run keeps their order.
type Pair struct {
	// Src is the source server S.
	Src *Env
	// Dst is the destination server D.
	Dst *Env
}

// NewPair starts two in-process servers for cli on store and returns them.
// Both run in-process as NewEnv does; the binary variant is out of scope.
// Each gets its own directory, database, blob store, tokens and namespace.
func NewPair(ctx context.Context, tb testing.TB, cli Install, store Store) *Pair {
	tb.Helper()

	parent := tb.TempDir()
	rec := &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}}

	srcDir := filepath.Join(parent, "src")
	dstDir := filepath.Join(parent, "dst")

	for _, dir := range []string{srcDir, dstDir} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			tb.Fatalf("create %s: %v", dir, err)
		}
	}

	src := newInprocessEnv(ctx, envInput{
		disableJournaling:      false,
		ipAllowlists:           false,
		signIn:                 false,
		signInSecretInVariable: false,
		signInRealTime:         false,
		passkeys:               false,
		journalRefuser:         nil,
		journalCap:             nil,
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    srcDir,
		token:                  newToken(tb),
		tokenB:                 newToken(tb),
		rec:                    rec,
		deltaCutoff:            0,
		sinkSenders:            nil,
		issuer:                 nil,
		ldap:                   nil,
	})
	dst := newInprocessEnv(ctx, envInput{
		disableJournaling:      false,
		ipAllowlists:           false,
		signIn:                 false,
		signInSecretInVariable: false,
		signInRealTime:         false,
		passkeys:               false,
		journalRefuser:         nil,
		journalCap:             nil,
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    dstDir,
		token:                  newToken(tb),
		tokenB:                 newToken(tb),
		rec:                    rec,
		deltaCutoff:            0,
		sinkSenders:            nil,
		issuer:                 nil,
		ldap:                   nil,
	})

	return &Pair{Src: src, Dst: dst}
}

// SharePulumiHome makes Dst use the home of Src, so one PULUMI_HOME holds
// the saved logins for both servers. Use it only for the Pulumi CLI, whose
// servers run over plain HTTP: OpenTofu homes hold per-server trust and
// credentials, which MergeTofuPair merges instead.
func (p *Pair) SharePulumiHome() {
	p.Dst.home = p.Src.home
}

// SavePulumiLogin saves the login for target in the shared home by running
// pulumi login <target URL> with the token of target for this run only.
// Later runs use StepSavedLogin with no token. It records stepName.
func (p *Pair) SavePulumiLogin(ctx context.Context, t *testing.T, target *Env, stepName string) Result {
	t.Helper()

	return p.Src.StepSavedLogin(ctx, t, StepSpec{
		Check:  nil,
		Name:   stepName,
		Args:   []string{"login", target.URL},
		Env:    []string{"PULUMI_ACCESS_TOKEN=" + target.token},
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
}

// UsePulumiBackend switches the current backend to target by running
// pulumi login <target URL> with the saved login. It records stepName.
func (p *Pair) UsePulumiBackend(ctx context.Context, t *testing.T, target *Env, stepName string) Result {
	t.Helper()

	return p.Src.StepSavedLogin(ctx, t, StepSpec{
		Check:  nil,
		Name:   stepName,
		Args:   []string{"login", target.URL},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
}

// MergeTofuPair trusts both servers and holds both credentials in each
// home. After it, each home has a bundle file with both certificates and a
// CLI configuration with both hosts, and each project still points at its
// own host: the Src project at S and the Dst project at D.
func (p *Pair) MergeTofuPair(tb testing.TB) {
	tb.Helper()

	bundle := filepath.Join(p.Src.home, "bundle.pem")
	writeBundle(tb, bundle, readCert(tb, p.Src), readCert(tb, p.Dst))

	p.Src.certBundle = bundle
	p.Dst.certBundle = bundle

	merged := fmt.Sprintf(tofuCLIConfig, p.Src.Host(), p.Src.token) +
		fmt.Sprintf(tofuCLIConfig, p.Dst.Host(), p.Dst.token)
	mergedB := fmt.Sprintf(tofuCLIConfig, p.Src.Host(), p.Src.tokenB) +
		fmt.Sprintf(tofuCLIConfig, p.Dst.Host(), p.Dst.tokenB)

	for _, home := range []string{p.Src.home, p.Dst.home} {
		writePairFile(tb, filepath.Join(home, "tofurc"), merged)
		writePairFile(tb, filepath.Join(home, "tofurc-b"), mergedB)
	}
}

// Steps returns the steps of both servers, source first. The smoke test runs
// through Src, so Dst holds none; merging still records both when a later
// test drives each side.
func (p *Pair) Steps() []StepResult {
	return append(append([]StepResult(nil), p.Src.steps...), p.Dst.steps...)
}

// NewPairRunAs collects the results of a pair run under label, so tests with
// their own scope write reports that never overwrite another test's file.
// The requests come from the shared recorder and hold both servers; the step
// commands hold both URLs.
func (p *Pair) NewPairRunAs(label string) Run {
	cli := p.Src.cli

	return Run{
		Tool:           cli.Tool,
		Label:          label,
		Store:          p.Src.store,
		Blobs:          p.Src.blobName,
		Version:        cli.Version,
		ChecksumSource: cli.ChecksumSource,
		Steps:          p.Steps(),
		Requests:       p.Src.Recorder.Entries(),
		Skipped:        p.Src.Skipped(),
	}
}

// NewPairRun collects the results of a pair run. The label gains a -pair
// suffix, so the file never overwrites a matrix report. The requests come
// from the shared recorder and hold both servers; the step commands hold
// both URLs.
func (p *Pair) NewPairRun() Run {
	return p.NewPairRunAs(p.Src.cli.Label + "-pair")
}

// readCert returns the PEM certificate Env trusts for its own server.
func readCert(tb testing.TB, env *Env) []byte {
	tb.Helper()

	raw, err := os.ReadFile(filepath.Join(env.home, "server.pem"))
	if err != nil {
		tb.Fatalf("read server certificate: %v", err)
	}

	return raw
}

// writeBundle writes certs a and b into a PEM bundle at path.
func writeBundle(tb testing.TB, path string, a, b []byte) {
	tb.Helper()

	combined := append(append([]byte(nil), a...), b...)
	if len(combined) == 0 || combined[len(combined)-1] != '\n' {
		combined = append(combined, '\n')
	}

	writePairFile(tb, path, string(combined))
}

// writePairFile writes content to name for pair helpers.
func writePairFile(tb testing.TB, name, content string) {
	tb.Helper()

	if err := os.WriteFile(name, []byte(content), fileMode); err != nil {
		tb.Fatalf("write %s: %v", name, err)
	}
}
