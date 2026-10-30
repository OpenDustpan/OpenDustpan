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
