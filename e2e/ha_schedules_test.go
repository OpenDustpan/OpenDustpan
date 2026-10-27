//go:build e2e

package e2e_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// haScheduleEvery is the interval that the test gives to each job schedule.
	haScheduleEvery = 10 * time.Second
	// haScheduleRun is how long the test observes the schedules.
	haScheduleRun = 60 * time.Second
	// haScheduleSettle bounds the wait for each schedule to tick after the
	// restart of the instances. A schedule waits a random first delay of up
	// to one minute before its first claim.
	haScheduleSettle = 3 * time.Minute
	// haScheduleFirstDelay is the longest first delay of a schedule loop plus
	// a margin. After this time since the last start, every loop of every
	// instance has made its schedule.
	haScheduleFirstDelay = 65 * time.Second
	// haScheduleMinTicks and haScheduleMaxTicks bound the ticks of one
	// schedule in the observed run: 6 at an interval of 10 s, less one for
	// the clock slack at each end and plus one for a tick at the very start.
	haScheduleMinTicks = 4
	haScheduleMaxTicks = 8
	// haTickLog is the message that an instance logs when it claims a tick.
	haTickLog = "durable schedule tick claimed"
)

// haFastSchedules is the test setting of the schedule interval: a trigger on
// the schedule table gives every schedule an interval of 10 s and a first tick
// within 10 s. The server has no setting for the interval of a job schedule,
// and it writes the interval of its own code when it starts, so a trigger in
// the test database is the one place that sets the interval for every
// instance. The trigger changes the database of this test only.
const haFastSchedules = `
CREATE OR REPLACE FUNCTION e2e_ha_fast_schedules() RETURNS trigger AS $$
BEGIN
  NEW.interval_ms := 10000;
  NEW.next_run_at_ms := LEAST(NEW.next_run_at_ms, (extract(epoch FROM clock_timestamp()) * 1000)::bigint + 10000);
  RETURN NEW;
END
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS e2e_ha_fast_schedules ON durable_schedules;
CREATE TRIGGER e2e_ha_fast_schedules BEFORE INSERT OR UPDATE ON durable_schedules
  FOR EACH ROW EXECUTE FUNCTION e2e_ha_fast_schedules();
UPDATE durable_schedules SET interval_ms = interval_ms;
`

// haTick is one tick of a job schedule: the operation that the tick started.
type haTick struct {
	ID       string
	StartKey string
	State    string
	Attempt  int64
}

// haScheduleRow is the stored state of one job schedule.
type haScheduleRow struct {
	Name     string
	Interval int64
	Claimed  int64
}

// haSchedules sets every job schedule to an interval of 10 s through a test
// setting, runs the instances for 60 s and checks that each tick ran on
// exactly one instance: one operation for each start key, and one claim in the
// logs of all instances for each operation.
func haSchedules(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)
	conn := haConnect(ctx, t, env)

	if _, err := conn.Exec(ctx, haFastSchedules); err != nil {
		t.Fatalf("set the schedule interval: %v", err)
	}

	// Each instance reads the interval at its start and then waits for its
	// next tick, so the instances start again one after the other. The
	// pool keeps serving through the others.
	for i := range cluster.Size() {
		if err := cluster.Stop(i); err != nil {
			t.Fatalf("stop instance %d: %v", i, err)
		}

		if err := cluster.Start(ctx, i); err != nil {
			t.Fatalf("start instance %d: %v", i, err)
		}
	}

	if err := cluster.WaitHealthy(ctx); err != nil {
		t.Fatalf("%v", err)
	}

	restarted := haDatabaseNow(ctx, t, conn)
	names := haWaitForSchedules(ctx, t, conn, restarted)

	t.Logf("schedules at an interval of %s: %v", haScheduleEvery, names)

	from := haDatabaseNow(ctx, t, conn)

	select {
	case <-ctx.Done():
		t.Fatalf("observe the schedules: %v", ctx.Err())
	case <-time.After(haScheduleRun):
	}

	to := haDatabaseNow(ctx, t, conn)

	haCheckTicks(t, cluster, names, haTicksBetween(ctx, t, conn, from, to))
}

// haDatabaseNow returns the clock of the database in milliseconds.
func haDatabaseNow(ctx context.Context, t *testing.T, conn *pgx.Conn) int64 {
	t.Helper()

	var now int64

	err := conn.QueryRow(ctx, `SELECT (extract(epoch FROM clock_timestamp()) * 1000)::bigint`).Scan(&now)
	if err != nil {
		t.Fatalf("read the database clock: %v", err)
	}

	return now
}

// haQuery runs a query and returns its rows as values of T, with the columns
// in the order of the fields.
func haQuery[T any](ctx context.Context, t *testing.T, conn *pgx.Conn, sql string, args ...any) []T {
	t.Helper()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("query the database: %v", err)
	}

	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[T])
	if err != nil {
		t.Fatalf("read the rows: %v", err)
	}

	return out
}

// haSchedulesReady reports whether every schedule has an interval of 10 s and
// a tick that an instance claimed after since.
func haSchedulesReady(rows []haScheduleRow, since int64) bool {
	for _, row := range rows {
		if row.Interval != haScheduleEvery.Milliseconds() || row.Claimed < since {
			return false
		}
	}

	return len(rows) > 0
}

// haWaitForSchedules waits until every schedule that the instances made has
// an interval of 10 s and a tick that an instance claimed after since, and
// until every loop of every instance has made its schedule. It returns the
// names of the schedules.
func haWaitForSchedules(ctx context.Context, t *testing.T, conn *pgx.Conn, since int64) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haScheduleSettle)
	defer cancel()

	for {
		rows := haQuery[haScheduleRow](ctx, t, conn,
			`SELECT name, interval_ms, COALESCE(last_claimed_at_ms, 0) FROM durable_schedules ORDER BY name`)

		if haSchedulesReady(rows, since) && haDatabaseNow(ctx, t, conn) >= since+haScheduleFirstDelay.Milliseconds() {
			names := make([]string, 0, len(rows))
			for _, row := range rows {
				names = append(names, row.Name)
			}

			return names
		}

		select {
		case <-ctx.Done():
			t.Fatalf("the schedules %v did not tick at an interval of %s: %v", rows, haScheduleEvery, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// haTicksBetween returns the operations that schedule ticks started from
// "from" (inclusive) up to "to" (exclusive) on the clock of the database. It
// waits for each of them to end, so the state is final.
func haTicksBetween(ctx context.Context, t *testing.T, conn *pgx.Conn, from, to int64) []haTick {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haLease+haBackupGrace)
	defer cancel()

	for {
		ticks := haQuery[haTick](ctx, t, conn,
			`SELECT id, start_key, state, attempt FROM durable_operations `+
				`WHERE start_key LIKE 'schedule:%' AND created_at_ms >= $1 AND created_at_ms < $2 `+
				`ORDER BY start_key`, from, to)

		if !slices.ContainsFunc(
			ticks,
			func(tick haTick) bool { return tick.State == "pending" || tick.State == "running" },
		) {
			return ticks
		}

		select {
		case <-ctx.Done():
			t.Fatalf("the ticks did not end: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// haCountClaims returns how many lines of the logs of all instances record the
// claim of the tick that started operation id, and the claims of each
// instance.
func haCountClaims(cluster *e2e.HACluster, id string, perInstance map[int]int) int {
	claims := 0

	for i := range cluster.Size() {
		n := haClaimLines(cluster.Logs(i), id)
		claims += n
		perInstance[i] += n
	}

	return claims
}

// haCheckTicks checks the ticks of the observed run: every schedule ticked
// about once for each interval, every start key has one operation, every
// operation succeeded on its first attempt, and exactly one instance claimed
// each tick.
func haCheckTicks(t *testing.T, cluster *e2e.HACluster, names []string, ticks []haTick) {
	t.Helper()

	perSchedule := map[string]int{}
	perKey := map[string]int{}
	perInstance := map[int]int{}

	for _, tick := range ticks {
		name, _, _ := strings.Cut(strings.TrimPrefix(tick.StartKey, "schedule:"), "@")
		perSchedule[name]++
		perKey[tick.StartKey]++

		if tick.State != "succeeded" || tick.Attempt != 1 {
			t.Errorf("tick %s (operation %s) reads state %q on attempt %d, want succeeded on attempt 1",
				tick.StartKey, tick.ID, tick.State, tick.Attempt)
		}

		if claims := haCountClaims(cluster, tick.ID, perInstance); claims != 1 {
			t.Errorf("tick %s (operation %s) was claimed %d times in the logs of the instances, want 1",
				tick.StartKey, tick.ID, claims)
		}
	}

	for key, n := range perKey {
		if n != 1 {
			t.Errorf("start key %s has %d operations, want 1", key, n)
		}
	}

	for _, name := range names {
		if n := perSchedule[name]; n < haScheduleMinTicks || n > haScheduleMaxTicks {
			t.Errorf("schedule %s ticked %d times in %s, want %d to %d",
				name, n, haScheduleRun, haScheduleMinTicks, haScheduleMaxTicks)
		}
	}

	t.Logf("ticks of the schedules in %s: %v", haScheduleRun, perSchedule)
	t.Logf("ticks claimed by each instance: %v", perInstance)
}

// haClaimLines counts the lines of logs that record the claim of the tick
// that started operation id.
func haClaimLines(logs, id string) int {
	count := 0

	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, haTickLog) && strings.Contains(line, id) {
			count++
		}
	}

	return count
}
