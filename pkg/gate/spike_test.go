package gate_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/gate"
	"opendustpan.org/opendustpan/pkg/audit"
	pkggate "opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const ownersFile = "# network changes need netops\n[network] @group:netops 1 modules/network/**\n"

var subject = pkggate.Subject{Tool: "opentofu", Namespace: "acme", Collection: "prod", Name: "prod/net"}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	full := append([]string{
		"-C", dir, "-c", "user.name=spike", "-c", "user.email=spike@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)

	out, err := exec.CommandContext(t.Context(), "git", full...).CombinedOutput() //nolint:gosec // fixed test arguments
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()

	p := filepath.Join(dir, name)

	err := os.MkdirAll(filepath.Dir(p), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(p, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// TestSpikeEndToEnd drives the steps of the spike plan through the engine,
// a real SQLite store (audit chain, fence), a real Git repository (the
// ownership file at the commit of the plan) and the HTTP route. It does not
// run the job path: see docs/spikes/policy-model.md.
func TestSpikeEndToEnd(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	store, openErr := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	region, err := namespace.ParseRegion("eu-west")
	if err != nil {
		t.Fatalf("region: %v", err)
	}

	err = store.CreateNamespace(ctx, namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "acme", HomeRegion: region, FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	write(t, repo, "OWNERS.dustpan", ownersFile)
	write(t, repo, "modules/network/main.tf", "# v1\n")
	write(t, repo, "modules/app/main.tf", "# v1\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "base")
	base := runGit(t, repo, "rev-parse", "HEAD")
	write(t, repo, "modules/network/main.tf", "# v2\n")
	runGit(t, repo, "commit", "-q", "-am", "change network")
	head := runGit(t, repo, "rev-parse", "HEAD")
	// The working tree must not matter: the file is read at the commit.
	write(t, repo, "OWNERS.dustpan", "# edited after the plan\n")

	src := pkggate.GitSource{Dir: repo}
	eng := pkggate.NewEngine()
	costs := pkggate.NewCostFacts()

	apply := func(rec pkggate.Record, who string) audit.Entry {
		t.Helper()

		_, clearErr := eng.Clear(ctx, rec.ID)
		if clearErr != nil {
			t.Fatalf("clear %s: %v", rec.ID, clearErr)
		}

		entry, appendErr := store.Append(ctx, fence, audit.Entry{
			At: time.Now().UTC(), Namespace: "acme", Subject: who, Action: "unit:apply", Object: subject.Ref(),
			Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: rec.ID,
			Changes: []audit.Change{{Key: "decision", New: rec.ID}},
		})
		if appendErr != nil {
			t.Fatalf("append audit: %v", appendErr)
		}

		return entry
	}

	plan := func(who pkggate.Principal) pkggate.Record {
		t.Helper()

		owners, ownErr := src.Owners(ctx, head)
		if ownErr != nil {
			t.Fatalf("owners: %v", ownErr)
		}

		paths, pathErr := src.ChangedPaths(ctx, base, head)
		if pathErr != nil {
			t.Fatalf("paths: %v", pathErr)
		}

		rec, planErr := eng.Planned(ctx, pkggate.PlannedRequest{
			Subject: subject, Principal: who, Owners: owners,
			Facts: pkggate.Facts{
				Operation: "apply",
				Unit:      pkggate.UnitFact{Collection: "prod", Labels: []string{"tier=core"}},
				Plan:      pkggate.PlanCounts{Update: 1},
				Cost:      costs.For(subject),
				Change:    pkggate.ChangeFact{Paths: paths, InputKeys: []string{}},
			},
		})
		if planErr != nil {
			t.Fatalf("plan: %v", planErr)
		}

		return rec
	}

	alice := pkggate.Principal{ID: "alice", Groups: []string{"dev"}}

	// Step 1: baseline apply with gates off.
	base1 := plan(alice)
	if base1.Result != pkggate.ResultAllow || len(base1.Matched) != 0 {
		t.Fatalf("step 1: record = %+v, want allow with no match", base1)
	}

	apply(base1, "alice")

	// Step 2: gates on, access limit on, cost pushed from a file.
	for _, g := range []pkggate.Gate{
		{
			ID: "acme/cost-warn", Scope: pkggate.Scope{Level: pkggate.LevelNamespace, Name: "acme"},
			DecisionPoint: pkggate.PointChangePlanned, Condition: "context.cost.deltaMonthly > 10000",
			Outcome: pkggate.OutcomeWarn, Message: "Monthly cost grows by more than 100.00 USD.", Mode: pkggate.ModeEnforce,
		},
		{
			ID: "acme/net-owners", Scope: pkggate.Scope{Level: pkggate.LevelCollection, Name: "acme/prod"},
			DecisionPoint: pkggate.PointChangePlanned, Condition: "true",
			Outcome: pkggate.ApprovalOutcome("owners"), Message: "Owners approve this change.", Mode: pkggate.ModeEnforce,
		},
	} {
		if putErr := eng.Registry.PutGate(g); putErr != nil {
			t.Fatalf("put gate %s: %v", g.ID, putErr)
		}
	}

	err = eng.Registry.PutLimit(pkggate.Limit{
		ID: "acme/no-prod-destroy", Scope: pkggate.Scope{Level: pkggate.LevelCollection, Name: "acme/prod"},
		Policy: `forbid (principal, action == Action::"unit:run-destroy", resource) ` +
			`when { resource.collection == "prod" } unless { principal in Group::"sre" };`,
	})
	if err != nil {
		t.Fatalf("put limit: %v", err)
	}

	cf, err := pkggate.ReadCostFile(filepath.Join("testdata", "cost-150.json"))
	if err != nil {
		t.Fatalf("read cost file: %v", err)
	}

	costs.Push(cf)

	// Step 3: the record shows require-approval with both matches.
	rec := plan(alice)
	if rec.Result != pkggate.ResultRequireApproval {
		t.Fatalf("step 3: result = %s, want require-approval", rec.Result)
	}

	if len(rec.Matched) != 2 || rec.Matched[0].Gate != "acme/cost-warn" || rec.Matched[1].Gate != "acme/net-owners" {
		t.Fatalf("step 3: matched = %+v, want cost-warn and net-owners", rec.Matched)
	}

	if len(rec.Approvals) != 1 || rec.Approvals[0].Rule != "owners/network" || rec.Approvals[0].Need != 1 {
		t.Fatalf("step 3: approvals = %+v, want owners/network need 1 (read at the commit)", rec.Approvals)
	}

	_, err = eng.Clear(ctx, rec.ID)
	if !errors.Is(err, pkggate.ErrNotCleared) {
		t.Fatalf("step 3: clear before approval: err = %v, want ErrNotCleared", err)
	}

	// The route reads the record.
	srv := http.NewServeMux()
	srv.Handle(
		gate.RoutePattern,
		gate.Handler(
			slog.New(slog.DiscardHandler),
			eng.Records,
			func(*http.Request, pkggate.Record) bool { return true },
		),
	)

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/spike/v1/decisions/"+rec.ID, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"result":"require-approval"`) ||
		!strings.Contains(rr.Body.String(), `"gate":"acme/cost-warn"`) {
		t.Fatalf("step 3: route = %d %s", rr.Code, rr.Body.String())
	}

	// Step 4: approvals.
	_, err = eng.Approve(ctx, rec.ID, pkggate.Approver{Principal: alice})
	if !errors.Is(err, pkggate.ErrAuthorApproval) {
		t.Fatalf("step 4: author approve: err = %v, want ErrAuthorApproval", err)
	}

	carol := pkggate.Principal{ID: "carol", Groups: []string{"dev"}}

	_, err = eng.Approve(ctx, rec.ID, pkggate.Approver{Principal: carol})
	if !errors.Is(err, pkggate.ErrNotApprover) || !strings.Contains(err.Error(), "owners/network") {
		t.Fatalf("step 4: carol approve: err = %v, want ErrNotApprover naming owners/network", err)
	}

	bob := pkggate.Principal{ID: "bob", Groups: []string{"netops"}}

	approved, err := eng.Approve(ctx, rec.ID, pkggate.Approver{Principal: bob})
	if err != nil || !approved.Cleared() {
		t.Fatalf("step 4: bob approve: err = %v, record = %+v", err, approved)
	}

	// Step 5: apply; the audit entry names the record.
	entry := apply(rec, "alice")
	if entry.RequestID != rec.ID || len(entry.Changes) != 1 || entry.Changes[0].New != rec.ID {
		t.Fatalf("step 5: audit entry = %+v, want the record ID %s", entry, rec.ID)
	}

	chk, err := store.Check(ctx, "acme")
	if err != nil || !chk.Valid {
		t.Fatalf("step 5: audit chain: err = %v, result = %+v", err, chk)
	}

	// Step 6: carol destroys; the access limit refuses.
	lrec, err := eng.CheckLimits(
		ctx,
		pkggate.LimitedRequest{Subject: subject, Principal: carol, Action: "unit:run-destroy"},
	)
	if !errors.Is(err, pkggate.ErrLimited) || !strings.Contains(err.Error(), "acme/no-prod-destroy") ||
		lrec.Result != pkggate.ResultDeny || len(lrec.Blockers) != 1 {
		t.Fatalf("step 6: err = %v, record = %+v, want ErrLimited naming the limit", err, lrec)
	}

	sre := pkggate.Principal{ID: "dave", Groups: []string{"sre"}}

	lrec, err = eng.CheckLimits(
		ctx,
		pkggate.LimitedRequest{Subject: subject, Principal: sre, Action: "unit:run-destroy"},
	)
	if err != nil || lrec.Result != pkggate.ResultAllow {
		t.Fatalf("step 6: sre destroy: err = %v, record = %+v, want allow", err, lrec)
	}

	// Step 7: evaluate mode records a match and changes nothing.
	err = eng.Registry.PutGate(pkggate.Gate{
		ID: "acme/no-deletes", Scope: pkggate.Scope{Level: pkggate.LevelNamespace, Name: "acme"},
		DecisionPoint: pkggate.PointChangePlanned, Condition: "context.plan.delete > 0",
		Outcome: pkggate.OutcomeDeny, Message: "No deletes.", Mode: pkggate.ModeEvaluate,
	})
	if err != nil {
		t.Fatalf("step 7: put gate: %v", err)
	}

	erec, err := eng.Planned(ctx, pkggate.PlannedRequest{
		Subject: subject, Principal: alice,
		Facts: pkggate.Facts{
			Operation: "apply", Unit: pkggate.UnitFact{Collection: "prod", Labels: []string{}},
			Plan: pkggate.PlanCounts{Delete: 2}, Change: pkggate.ChangeFact{Paths: []string{}, InputKeys: []string{}},
		},
	})
	if err != nil || erec.Result != pkggate.ResultAllow || len(erec.Blockers) != 0 {
		t.Fatalf("step 7: err = %v, record = %+v, want allow", err, erec)
	}

	found := false

	for _, m := range erec.Matched {
		found = found || (m.Gate == "acme/no-deletes" && m.Mode == pkggate.ModeEvaluate)
	}

	if !found {
		t.Fatalf("step 7: matched = %+v, want acme/no-deletes in evaluate mode", erec.Matched)
	}

	// Step 8: a gate that reads an unknown fact stops the change.
	bad := pkggate.NewEngine()

	err = bad.Registry.PutGate(pkggate.Gate{
		ID: "acme/unknown-fact", Scope: pkggate.Scope{Level: pkggate.LevelNamespace, Name: "acme"},
		DecisionPoint: pkggate.PointChangePlanned, Condition: "context.risk.score > 3",
		Outcome: pkggate.OutcomeDeny, Message: "Risk.", Mode: pkggate.ModeEnforce,
	})
	if err != nil {
		t.Fatalf("step 8: put gate: %v", err)
	}

	brec, err := bad.Planned(ctx, pkggate.PlannedRequest{Subject: subject, Principal: alice, Facts: pkggate.Facts{
		Operation: "apply", Unit: pkggate.UnitFact{Collection: "prod", Labels: []string{}},
		Change: pkggate.ChangeFact{Paths: []string{}, InputKeys: []string{}},
	}})
	if !errors.Is(err, pkggate.ErrRuleFailed) || brec.Result != pkggate.ResultError || len(brec.Errors) != 1 ||
		!strings.Contains(brec.Errors[0], "acme/unknown-fact") {
		t.Fatalf("step 8: err = %v, record = %+v, want ErrRuleFailed and the error in the record", err, brec)
	}

	if _, clearErr := bad.Clear(ctx, brec.ID); !errors.Is(clearErr, pkggate.ErrNotCleared) {
		t.Fatalf("step 8: clear of an error record: err = %v, want ErrNotCleared", clearErr)
	}
}

// Step 9 is TestOfflineRunner.
func TestOfflineRunner(t *testing.T) {
	t.Parallel()

	for file, wantFailed := range map[string]int{"pass.gatetest.json": 0, "fail.gatetest.json": 1} {
		results, err := pkggate.RunTestFile(t.Context(), filepath.Join("testdata", file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}

		failed := 0

		for _, r := range results {
			if !r.Passed {
				failed++
			}
		}

		if failed != wantFailed || len(results) != 2 {
			t.Fatalf(
				"%s: %d cases, %d failed, want 2 cases and %d failed: %+v",
				file,
				len(results),
				failed,
				wantFailed,
				results,
			)
		}
	}
}
