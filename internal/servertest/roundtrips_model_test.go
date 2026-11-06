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
		newGrowthCase("one more transaction in the large run",
			map[string]int{"GetStack": 5, "begin": 21, "commit": 21}, nil, "query begin", "query commit"),
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
// The first three commits fitted the coefficients of the models. The last one
// did not: the models predict it, and heldOut marks it.
func measuredHistory() []measuredPoint {
	return []measuredPoint{
		{"a839dc5", schemaCounts{tables: 76, otherTables: 18, migrations: 48, units: 3}, 301, 998, false},
		{"a839dc5", schemaCounts{tables: 76, otherTables: 18, migrations: 48, units: 30}, 382, 1133, false},
		{"778d3d8", schemaCounts{tables: 78, otherTables: 18, migrations: 50, units: 3}, 307, 1016, false},
		{"778d3d8", schemaCounts{tables: 78, otherTables: 18, migrations: 50, units: 30}, 388, 1151, false},
		{"1ebfe03", schemaCounts{tables: 78, otherTables: 19, migrations: 51, units: 3}, 308, 1020, false},
		{"1ebfe03", schemaCounts{tables: 78, otherTables: 19, migrations: 51, units: 30}, 389, 1155, false},
		{"19266b1", schemaCounts{tables: 80, otherTables: 19, migrations: 52, units: 3}, 314, 1041, true},
		{"19266b1", schemaCounts{tables: 80, otherTables: 19, migrations: 52, units: 30}, 395, 1176, true},
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

// fitTolerance is the number of round trips by which the change that a model
// gives between two commits may differ from the measured change. Two things
// move a measured change: the handover of the operation under load, which the
// history still shows because it was measured before the counts became
// exact, and a change that is not a table, such as a new step. The held-out
// commit added a restore step, and the model is 5 below it.
const fitTolerance = 6

// TestCostModelFitsMeasuredHistory checks the coefficients of the models
// against the measured counts. The fixed part of a model holds the steps of
// the flow, which change between commits, so the check compares the change
// from the first commit to each later commit, with the same data set: the
// fixed part drops out, and the change that the coefficients give must be
// near the measured change. The held-out point is a count that the fit did
// not use, so it tests what the models predict and not what they remember.
// TestRoundTripsOfBackup checks the fixed part, with the exact count of the
// current commit.
func TestCostModelFitsMeasuredHistory(t *testing.T) {
	t.Parallel()

	history := measuredHistory()

	for _, point := range history {
		base := history[slices.IndexFunc(history, func(p measuredPoint) bool {
			return p.schema.units == point.schema.units
		})]

		name := fmt.Sprintf("%s with %d units", point.commit, point.schema.units)
		if point.heldOut {
			name += " (held out)"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, flow := range []struct {
				name        string
				model       costModel
				trips, from int
			}{
				{"backup", backupModel(), point.backupTrips, base.backupTrips},
				{"restore", restoreModel(), point.restoreTrip, base.restoreTrip},
			} {
				predicted := flow.model.count(point.schema) - flow.model.count(base.schema)
				measured := flow.trips - flow.from

				if gap := predicted - measured; gap < -fitTolerance || gap > fitTolerance {
					t.Errorf("%s: from %s the model predicts %d more round trips and the measured change is %d, "+
						"tolerance %d", flow.name, base.commit, predicted, measured, fitTolerance)
				}
			}
		})
	}
}

// swapAttempt returns the round trips of one attempt of the swap step: its
// first statement, then n more.
func swapAttempt(n int) []roundTrip {
	return slices.Concat([]roundTrip{queryTrip(swapStartSQL)}, perRowTrips("swap statement", n))
}

// TestWithoutSwapRetries checks that the count keeps the last attempt of the
// swap step and the round trips around it, and nothing else.
func TestWithoutSwapRetries(t *testing.T) {
	t.Parallel()

	before := perRowTrips("before", 3)
	after := perRowTrips("after", 2)

	cases := []struct {
		name string
		in   []roundTrip
		want []roundTrip
	}{
		{"no swap", slices.Concat(before, after), slices.Concat(before, after)},
		{"one attempt", slices.Concat(before, swapAttempt(4), after), slices.Concat(before, swapAttempt(4), after)},
		{
			"one failed attempt",
			slices.Concat(before, swapAttempt(7), perRowTrips("claim end", 6), swapAttempt(4), after),
			slices.Concat(before, swapAttempt(4), after),
		},
		{
			"two failed attempts",
			slices.Concat(before, swapAttempt(7), swapAttempt(2), swapAttempt(4), after),
			slices.Concat(before, swapAttempt(4), after),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := withoutSwapRetries(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("withoutSwapRetries gives %d round trips, want %d\n%s", len(got), len(tc.want), listTrips(got))
			}
		})
	}
}

// TestWithoutSwapRetriesKeepsOtherExtraWork is the mutation check of the
// filter: a statement that a change adds to the restore, outside of a failed
// attempt, must stay in the count.
func TestWithoutSwapRetriesKeepsOtherExtraWork(t *testing.T) {
	t.Parallel()

	in := slices.Concat(
		perRowTrips("before", 3), swapAttempt(4), perRowTrips("new statement", 1), perRowTrips("after", 2),
	)

	if got := len(withoutSwapRetries(in)); got != len(in) {
		t.Errorf("withoutSwapRetries gives %d round trips, want %d: an added statement was dropped", got, len(in))
	}
}
