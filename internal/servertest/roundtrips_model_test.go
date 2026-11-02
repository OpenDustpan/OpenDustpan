package servertest_test

import (
	"fmt"
	"slices"
	"testing"
)

// This file tests the rules of roundtrips_test.go with made-up round trips, so
// that it needs no database. A rule that the real flow does not break could be
// a rule that checks nothing, so each rule has a case that breaks it.

// queryTrip returns one round trip of the statement sql.
func queryTrip(sql string) roundTrip {
	return roundTrip{kind: "query", sql: sql, frame: "test"}
}

// trips returns the round trips of a flow as a list of statements, each with
// its count.
func trips(counts map[string]int) []roundTrip {
	out := make([]roundTrip, 0, len(counts))

	for statement, n := range counts {
		out = append(out, perRowTrips(statement, n)...)
	}

	return out
}

// growthCase is one case of the growth rule: the counts of the large run, the
// allowance, and the statements that the rule must name, in order.
type growthCase struct {
	name      string
	large     map[string]int
	allowance map[string]int
	wantBad   []string
}

func newGrowthCase(name string, large, allowance map[string]int, wantBad ...string) growthCase {
	return growthCase{name: name, large: large, allowance: allowance, wantBad: wantBad}
}

func TestGrowthRule(t *testing.T) {
	t.Parallel()

	base := map[string]int{"GetStack": 5, "begin": 20, "commit": 20}

	cases := []growthCase{
		newGrowthCase("no growth", map[string]int{"GetStack": 5, "begin": 20, "commit": 20}, nil),
		newGrowthCase("a handover repeats a few statements",
			map[string]int{"GetStack": 5, "begin": 22, "commit": 23}, nil),
		newGrowthCase("a loop that queries once for each stack",
			map[string]int{"GetStack": 32, "begin": 20, "commit": 20}, nil, "query GetStack"),
		newGrowthCase("a new statement for each row",
			map[string]int{"GetStack": 5, "begin": 20, "commit": 20, "ListOutputs": 30}, nil, "query ListOutputs"),
		newGrowthCase("a transaction for each row",
			map[string]int{"GetStack": 5, "begin": 47, "commit": 47}, nil, "query begin", "query commit"),
		newGrowthCase("the known growth stays inside its allowance",
			map[string]int{"GetStack": 59, "begin": 20, "commit": 20}, map[string]int{"query GetStack": 54}),
		newGrowthCase("the known growth doubles",
			map[string]int{"GetStack": 113, "begin": 20, "commit": 20}, map[string]int{"query GetStack": 54},
			"query GetStack"),
		newGrowthCase("a statement that only shrinks", map[string]int{"GetStack": 1, "begin": 20, "commit": 20}, nil),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			problems := growthProblems(trips(base), trips(tc.large), tc.allowance)

			if len(problems) != len(tc.wantBad) {
				t.Fatalf("problems = %q, want statements %q", problems, tc.wantBad)
			}

			for i, want := range tc.wantBad {
				if got := problems[i]; len(got) < len(want) || got[:len(want)] != want {
					t.Errorf("problem %d = %q, want it to start with %q", i, got, want)
				}
			}
		})
	}
}

// TestGrowthRuleHidesSchemaNames checks that the restore, which makes schemas
// with random names, counts one statement and not one for each name.
func TestGrowthRuleHidesSchemaNames(t *testing.T) {
	t.Parallel()

	small := []roundTrip{
		queryTrip(`ALTER SCHEMA "restore_stage_aaaa_1" RENAME TO "restore_prev_bbbb"`),
		queryTrip(`SELECT COUNT(*) FROM "restore_stage_aaaa_1"."stacks" AS t LEFT JOIN "restore_stage_aa...`),
	}
	large := slices.Concat(small, small, small, small, small, small, small, small)

	if problems := growthProblems(small, large, nil); len(problems) != 2 {
		t.Fatalf("problems = %q, want one for each statement that repeats", problems)
	}

	other := []roundTrip{
		queryTrip(`ALTER SCHEMA "restore_stage_cccc_1" RENAME TO "restore_prev_dddd"`),
		queryTrip(`SELECT COUNT(*) FROM "restore_stage_cccc_1"."stacks" AS t LEFT JOIN "restore_stage_cc...`),
	}

	if problems := growthProblems(small, other, nil); len(problems) != 0 {
		t.Fatalf("a run with other schema names gave problems %q", problems)
	}
}

// measuredHistory holds the counts of a backup and of a restore at four
// commits that differ in tables, control tables and migrations, each with 3 and
// with 30 stacks and workspaces. The tracer of roundtrips_test.go measured them.
// The tracer then counted the round trips of the durable runner for its own
// bookkeeping, and it does not count them now (see isRunnerBookkeeping). Each
// count is the measured count minus the bookkeeping of the flow at the current
// commit: 24 round trips for a backup and 102 for a restore, the same for 3 and
// for 30 units. A commit with other restore steps had a little less
// bookkeeping, and fitTolerance covers that. The first three commits fitted the
// coefficients of the models. The last one did not: the models predict it, and
// heldOut marks it.
func measuredHistory() []measuredPoint {
	return []measuredPoint{
		{"a839dc5", schemaCounts{tables: 76, otherTables: 18, migrations: 48, units: 3}, 277, 896, false},
		{"a839dc5", schemaCounts{tables: 76, otherTables: 18, migrations: 48, units: 30}, 358, 1031, false},
		{"778d3d8", schemaCounts{tables: 78, otherTables: 18, migrations: 50, units: 3}, 283, 914, false},
		{"778d3d8", schemaCounts{tables: 78, otherTables: 18, migrations: 50, units: 30}, 364, 1049, false},
		{"1ebfe03", schemaCounts{tables: 78, otherTables: 19, migrations: 51, units: 3}, 284, 918, false},
		{"1ebfe03", schemaCounts{tables: 78, otherTables: 19, migrations: 51, units: 30}, 365, 1053, false},
		{"19266b1", schemaCounts{tables: 80, otherTables: 19, migrations: 52, units: 3}, 290, 939, true},
		{"19266b1", schemaCounts{tables: 80, otherTables: 19, migrations: 52, units: 30}, 371, 1074, true},
	}
}

// measuredPoint is the counts of one commit with one data set. heldOut is true
// for a point that did not fit the coefficients.
type measuredPoint struct {
	commit                   string
	schema                   schemaCounts
	backupTrips, restoreTrip int
	heldOut                  bool
}

// fitTolerance is the number of round trips by which a model may differ from a
// measured count. Two things move a count: the handover of the operation under
// load, and a change that is not a table, such as a new step. The held-out
// commit added a restore step, and the model is 5 below it.
const fitTolerance = 6

// TestCostModelFitsMeasuredHistory checks the models against the measured
// counts. For each point the ceiling must be above the count, and the model
// without its margin must be near the count. The held-out point is a count that
// the fit did not use, so it tests what the models predict and not what they
// remember. A model that is far above the data would not catch a change that
// adds round trips.
func TestCostModelFitsMeasuredHistory(t *testing.T) {
	t.Parallel()

	for _, point := range measuredHistory() {
		name := fmt.Sprintf("%s with %d units", point.commit, point.schema.units)
		if point.heldOut {
			name += " (held out)"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, flow := range []struct {
				name  string
				model costModel
				trips int
			}{
				{"backup", backupModel(), point.backupTrips},
				{"restore", restoreModel(), point.restoreTrip},
			} {
				ceiling := flow.model.ceiling(point.schema)

				if flow.trips > ceiling {
					t.Errorf("%s: measured %d round trips, ceiling %d", flow.name, flow.trips, ceiling)
				}

				if gap := ceiling - flow.model.margin - flow.trips; gap < -fitTolerance || gap > fitTolerance {
					t.Errorf("%s: the model predicts %d round trips and the measured count is %d, tolerance %d",
						flow.name, ceiling-flow.model.margin, flow.trips, fitTolerance)
				}
			}
		})
	}
}

// TestRunnerBookkeepingRule checks the rule that tells the round trips of the
// durable runner from the work of a step function, with made-up stacks.
func TestRunnerBookkeepingRule(t *testing.T) {
	t.Parallel()

	const (
		pgx    = "github.com/jackc/pgx/v5.(*Conn).Exec"
		db     = "opendustpan.org/opendustpan/internal/db.(*Queries).InsertDurableStep"
		store  = "opendustpan.org/opendustpan/pkg/store/postgres.(*DurableOperations).RecordStep"
		runner = "opendustpan.org/opendustpan/pkg/durable.(*Runner).record"
		step   = "opendustpan.org/opendustpan/pkg/backup.(*Service).stepFinish"
		worker = "opendustpan.org/opendustpan/pkg/backup.(*Service).Work.func1"
	)

	cases := []struct {
		name  string
		stack []string
		want  bool
	}{
		{"the runner records a step", []string{pgx, db, store, runner, worker}, true},
		{"the same record under another worker", []string{pgx, db, store, runner, "other.Work"}, true},
		{"a step function writes under the runner", []string{pgx, db, store, step, runner, worker}, false},
		{"a function of the store with no caller", []string{pgx, db, store}, false},
		{"a driver call from a pkg/backup function", []string{pgx, step, runner}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isRunnerBookkeeping(tc.stack); got != tc.want {
				t.Fatalf("isRunnerBookkeeping(%v) = %v, want %v", tc.stack, got, tc.want)
			}
		})
	}
}

// TestWorkerOfRule checks which worker of pkg/backup the tracer names for a
// stack, with made-up stacks.
func TestWorkerOfRule(t *testing.T) {
	t.Parallel()

	const (
		runner    = "opendustpan.org/opendustpan/pkg/durable.(*Runner).Run"
		backupSvc = "opendustpan.org/opendustpan/pkg/backup.(*Service).Work.func1"
		restore   = "opendustpan.org/opendustpan/pkg/backup.(*RestoreService).Work.func1"
		await     = "opendustpan.org/opendustpan/pkg/backup.(*Service).Await"
	)

	cases := []struct {
		name  string
		stack []string
		want  string
	}{
		{"the worker of the backup", []string{runner, backupSvc}, workerBackup},
		{"the worker of the restore", []string{runner, restore}, workerRestore},
		{"a poll of Await has no worker", []string{runner, await}, ""},
		{"no pkg/backup frame", []string{runner}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := workerOf(tc.stack); got != tc.want {
				t.Fatalf("workerOf(%v) = %q, want %q", tc.stack, got, tc.want)
			}
		})
	}
}

// TestRunnerGrowthRule checks the rule that the runner round trips do not grow
// with the units, with made-up runs.
func TestRunnerGrowthRule(t *testing.T) {
	t.Parallel()

	run := func(restoreN, backupN int) measuredRun {
		return measuredRun{
			backup: nil, restore: nil, backupGrowth: nil, restoreGrowth: nil,
			restoreWorker: perRowTrips("InsertDurableStep", restoreN),
			backupWorker:  perRowTrips("InsertDurableStep", backupN),
		}
	}

	cases := []struct {
		name         string
		small, large measuredRun
		want         int
	}{
		{"the same", run(104, 28), run(104, 28), 0},
		{"the spread of the runs", run(102, 24), run(110, 33), 0},
		{"a record for each unit of the restore", run(104, 28), run(131, 28), 1},
		{"a record for each unit of both", run(104, 28), run(131, 55), 2},
		{"fewer round trips", run(110, 33), run(102, 24), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := runnerGrowthProblems(tc.small, tc.large); len(got) != tc.want {
				t.Fatalf("problems = %q, want %d", got, tc.want)
			}
		})
	}
}
