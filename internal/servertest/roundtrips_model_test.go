package servertest_test

import (
	"slices"
	"testing"
)

// This file tests the rules of roundtrips_test.go with made-up round trips, so
// that it needs no database. A rule that the real flow does not break could be
// a rule that checks nothing, so each rule has a case that breaks it.

// trips returns the round trips of a flow as a list of statements, each with
// its count.
// queryTrip returns one round trip of the statement sql.
func queryTrip(sql string) roundTrip {
	return roundTrip{kind: "query", sql: sql, frame: "test"}
}

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

// measuredHistory holds the counts that a run of the flows made at three
// commits that differ in the number of tables, control tables and migrations.
// The tracer of this file measured them with 3 stacks and 3 workspaces.
func measuredHistory() []measuredPoint {
	return []measuredPoint{
		{"a839dc5", schemaCounts{tables: 76, otherTables: 18, migrations: 48, stacks: 3}, 292, 969},
		{"778d3d8", schemaCounts{tables: 78, otherTables: 18, migrations: 50, stacks: 3}, 298, 987},
		{"1ebfe03", schemaCounts{tables: 78, otherTables: 19, migrations: 51, stacks: 3}, 299, 991},
	}
}

// measuredPoint is the counts of one commit.
type measuredPoint struct {
	commit                   string
	schema                   schemaCounts
	backupTrips, restoreTrip int
}

// TestCostModelFitsMeasuredHistory checks that the ceilings of the model stay
// above the measured counts of earlier commits, and not far above them. A model
// with a wide margin would not catch a change that adds round trips.
func TestCostModelFitsMeasuredHistory(t *testing.T) {
	t.Parallel()

	for _, point := range measuredHistory() {
		t.Run(point.commit, func(t *testing.T) {
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

				if ceiling-flow.trips != flow.model.margin {
					t.Errorf("%s: ceiling %d is %d above the measured %d, want the margin %d",
						flow.name, ceiling, ceiling-flow.trips, flow.trips, flow.model.margin)
				}
			}
		})
	}
}

// TestCostModelNewTable checks that a change which adds tables with the usual
// cost needs no edit: one more table with its migration, and one more table
// that a backup leaves out, each cost what the model says and stay under the
// ceiling.
func TestCostModelNewTable(t *testing.T) {
	t.Parallel()

	history := measuredHistory()
	head := history[len(history)-1]

	for added := range 6 {
		schema := schemaCounts{
			tables:      head.schema.tables + added,
			otherTables: head.schema.otherTables + added,
			migrations:  head.schema.migrations + 2*added,
			stacks:      head.schema.stacks,
		}

		backupTrips := head.backupTrips + added*(backupModel().perTable+backupModel().perOther)
		restoreTrips := head.restoreTrip + added*(restoreModel().perTable+restoreModel().perOther) +
			2*added*restoreModel().perMigration

		if ceiling := backupModel().ceiling(schema); backupTrips > ceiling {
			t.Errorf("%d new tables: backup %d round trips, ceiling %d", added, backupTrips, ceiling)
		}

		if ceiling := restoreModel().ceiling(schema); restoreTrips > ceiling {
			t.Errorf("%d new tables: restore %d round trips, ceiling %d", added, restoreTrips, ceiling)
		}
	}
}

// TestCostModelCatchesFixedCost checks that a change which adds round trips
// with no new table fails the ceiling once it goes past the margin.
func TestCostModelCatchesFixedCost(t *testing.T) {
	t.Parallel()

	history := measuredHistory()
	head := history[len(history)-1]

	if got, ceiling := head.restoreTrip+restoreModel().margin+1, restoreModel().ceiling(head.schema); got <= ceiling {
		t.Errorf("restore with %d round trips passes the ceiling %d, want it to fail", got, ceiling)
	}

	if got, ceiling := head.backupTrips+backupModel().margin+1, backupModel().ceiling(head.schema); got <= ceiling {
		t.Errorf("backup with %d round trips passes the ceiling %d, want it to fail", got, ceiling)
	}
}
