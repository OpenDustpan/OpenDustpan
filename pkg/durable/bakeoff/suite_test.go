package bakeoff_test

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/pkg/durable"
)

func TestMain(m *testing.M) {
	if os.Getenv("BAKEOFF_HELPER") == "kill" {
		helperKill()

		return
	}

	goleak.VerifyTestMain(m)
}

// helperKill is instance A in its own process: it starts the operation of
// BAKEOFF_OP, blocks in step s2, and waits to be killed.
func helperKill() {
	b := backingFromEnv()
	ctx := context.Background()

	_, _ = setBlock("A/s2")

	e, err := open(ctx, b, instanceOpts{id: "A", executorID: os.Getenv("BAKEOFF_EXECUTOR"), claimTTL: time.Second})
	if err != nil {
		os.Stdout.WriteString("HELPER-ERROR " + err.Error() + "\n")
		os.Exit(2)
	}

	op := os.Getenv("BAKEOFF_OP")
	if _, err := e.Start(ctx, op, "three", []byte(op)); err != nil {
		os.Stdout.WriteString("HELPER-ERROR " + err.Error() + "\n")
		os.Exit(2)
	}

	select {}
}

// spawnAndKill runs instance A in a helper process, waits until it is inside
// step s2, and kills it with SIGKILL.
func spawnAndKill(t *testing.T, b *backing, op, executor string, beforeKill func()) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), b.env()...)
	cmd.Env = append(cmd.Env, "BAKEOFF_HELPER=kill", "BAKEOFF_OP="+op, "BAKEOFF_EXECUTOR="+executor)

	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	ready := make(chan string, 1)

	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "STEP-S2-STARTED") || strings.HasPrefix(line, "HELPER-ERROR") {
				ready <- line

				break
			}
		}
		// Drain the rest so the helper never blocks on a full pipe.
		for sc.Scan() {
		}
	}()

	select {
	case line := <-ready:
		if strings.HasPrefix(line, "HELPER-ERROR") {
			t.Fatal(line)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("helper did not reach step s2")
	}

	if beforeKill != nil {
		beforeKill()
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func mustOpen(t *testing.T, b *backing, o instanceOpts) durable.Engine {
	t.Helper()

	e, err := open(t.Context(), b, o)
	if err != nil {
		t.Fatalf("open %s: %v", o.id, err)
	}

	return e
}

func closeEngine(t *testing.T, e durable.Engine) {
	t.Helper()

	if err := e.Close(context.Background()); err != nil {
		t.Errorf("close: %v", err)
	}
}

// result logs one line of the bake-off table and fails the test when ok is
// false.
func result(t *testing.T, b *backing, name string, ok bool, detail string) {
	t.Helper()

	verdict := "PASS"
	if !ok {
		verdict = "FAIL"
		t.Errorf("%s %s %s: %s", b.adapter, b.store, name, detail)
	}

	t.Logf("RESULT | %s | %s | %s | %s | %s", b.adapter, b.store, name, verdict, detail)
}

func TestBakeoff(t *testing.T) {
	for _, adapter := range []string{"own", "dbos"} {
		for _, store := range []string{"postgres", "sqlite"} {
			t.Run(adapter+"/"+store, func(t *testing.T) {
				t.Run("start key", func(t *testing.T) { caseStartKey(t, newBacking(t, adapter, store)) })
				t.Run("status from another instance", func(t *testing.T) { caseStatusElsewhere(t, newBacking(t, adapter, store)) })
				t.Run("fenced step rolls back", func(t *testing.T) { caseFenced(t, newBacking(t, adapter, store)) })
				t.Run("kill mid-step, peer running", func(t *testing.T) { caseKill(t, newBacking(t, adapter, store), false) })
				if adapter == "dbos" {
					t.Run("kill mid-step, peer starts later with the same executor ID", func(t *testing.T) {
						caseKill(t, newBacking(t, adapter, store), true)
					})
				}
				t.Run("a start leaves live work alone", func(t *testing.T) { caseLiveUntouched(t, newBacking(t, adapter, store)) })
				t.Run("stalled holder is fenced", func(t *testing.T) { caseZombie(t, newBacking(t, adapter, store)) })
				t.Run("schedule fires once per tick", func(t *testing.T) { caseSchedule(t, newBacking(t, adapter, store)) })
			})
		}
	}
}

func caseStartKey(t *testing.T, b *backing) {
	a := mustOpen(t, b, instanceOpts{id: "A"})
	defer closeEngine(t, a)

	id1, err1 := a.Start(t.Context(), "k1", "three", []byte("k1"))
	id2, err2 := a.Start(t.Context(), "k1", "three", []byte("k1"))
	s := waitState(t, a, id1, durable.StateSucceeded, 15*time.Second)

	ok := err1 == nil && err2 == nil && id1 == id2 && s.State == durable.StateSucceeded &&
		count(t, b, "k1", "s1") == 1 && count(t, b, "k1", "s2") == 1 && count(t, b, "k1", "s3") == 1
	result(t, b, "start key", ok, "ids "+id1+" "+id2+", state "+string(s.State)+errText(err1, err2))
}

func caseStatusElsewhere(t *testing.T, b *backing) {
	a := mustOpen(t, b, instanceOpts{id: "A"})
	defer closeEngine(t, a)

	bb := mustOpen(t, b, instanceOpts{id: "B"})
	defer closeEngine(t, bb)

	id, err := a.Start(t.Context(), "k2", "three", []byte("k2"))
	s := waitState(t, bb, id, durable.StateSucceeded, 15*time.Second)
	result(t, b, "status from another instance", err == nil && s.State == durable.StateSucceeded,
		"state seen by B: "+string(s.State)+errText(err))
}

func caseFenced(t *testing.T, b *backing) {
	if _, err := b.db.ExecContext(t.Context(), `UPDATE spike_fence SET version = 2`); err != nil {
		t.Fatal(err)
	}

	a := mustOpen(t, b, instanceOpts{id: "A"})
	defer closeEngine(t, a)

	id, err := a.Start(t.Context(), "f1", "fenced", []byte("f1"))
	s := waitState(t, a, id, durable.StateFailed, 15*time.Second)
	rows := count(t, b, "f1", "fenced")
	result(t, b, "fenced step rolls back", err == nil && s.State == durable.StateFailed && rows == 0,
		"state "+string(s.State)+", domain rows "+itoa(rows)+errText(err))
}

func caseKill(t *testing.T, b *backing, sameExecutor bool) {
	op := "kill1"
	executorA := ""
	executorB := ""

	if sameExecutor {
		executorA, executorB = "local", "local"
	}

	var peer durable.Engine

	var openPeer func()
	// On SQLite the stalled step of A holds the one write lock, and opening
	// B waits for it, so B opens after the kill there.
	if !sameExecutor && b.store == "postgres" {
		// B runs before A dies: it opens once A is inside step s2.
		openPeer = func() { peer = mustOpen(t, b, instanceOpts{id: "B", claimTTL: time.Second}) }
	}

	spawnAndKill(t, b, op, executorA, openPeer)

	if !sameExecutor && b.store == "sqlite" {
		peer = mustOpen(t, b, instanceOpts{id: "B", claimTTL: time.Second})
	}

	if !sameExecutor {
		defer closeEngine(t, peer)
	}

	if sameExecutor {
		peer = mustOpen(t, b, instanceOpts{id: "B", executorID: executorB, claimTTL: time.Second})
		defer closeEngine(t, peer)
	}

	var s durable.Status
	if b.adapter == "own" {
		// Our adapter's ID is not the key; find it by key.
		s = waitOwnByKey(t, b, peer, op)
	} else {
		s = waitState(t, peer, op, durable.StateSucceeded, 20*time.Second)
	}

	s1, s2, s3 := count(t, b, op, "s1"), count(t, b, op, "s2"), count(t, b, op, "s3")
	ok := s.State == durable.StateSucceeded && s1 == 1 && s2 == 1 && s3 == 1
	name := "kill mid-step, peer running"

	if sameExecutor {
		name = "kill mid-step, peer starts later with the same executor ID"
	}

	result(t, b, name, ok, "state "+string(s.State)+", holder "+s.Holder+", rows s1/s2/s3 "+
		itoa(s1)+"/"+itoa(s2)+"/"+itoa(s3))
}

func waitOwnByKey(t *testing.T, b *backing, e durable.Engine, key string) durable.Status {
	t.Helper()

	var id string

	deadline := time.Now().Add(20 * time.Second)

	for id == "" && time.Now().Before(deadline) {
		_ = b.db.QueryRowContext(t.Context(), q(b, `SELECT id FROM durable_operations WHERE start_key = ?`), key).Scan(&id)
		time.Sleep(50 * time.Millisecond)
	}

	return waitState(t, e, id, durable.StateSucceeded, time.Until(deadline))
}

func caseLiveUntouched(t *testing.T, b *backing) {
	skipDBOSSQLiteStall(t, b)

	release, started := setBlock("A/s2")
	defer release()

	a := mustOpen(t, b, instanceOpts{id: "A", executorID: "local", claimTTL: time.Second})
	defer closeEngine(t, a)

	id, err := a.Start(t.Context(), "live1", "three", []byte("live1"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("step s2 did not start")
	}

	// A second instance starts while A runs. For DBOS it uses the default
	// shape of one executor ID on every instance.
	bb := mustOpen(t, b, instanceOpts{id: "B", executorID: "local", claimTTL: time.Second})
	defer closeEngine(t, bb)

	time.Sleep(2500 * time.Millisecond)
	release()

	s := waitState(t, a, id, durable.StateSucceeded, 15*time.Second)
	s1, s2, s3 := count(t, b, "live1", "s1"), count(t, b, "live1", "s2"), count(t, b, "live1", "s3")
	ok := s.State == durable.StateSucceeded && s1 == 1 && s2 == 1 && s3 == 1
	result(t, b, "a start leaves live work alone", ok, "state "+string(s.State)+", rows s1/s2/s3 "+
		itoa(s1)+"/"+itoa(s2)+"/"+itoa(s3))
}

func caseZombie(t *testing.T, b *backing) {
	skipDBOSSQLiteStall(t, b)

	release, started := setBlock("A/s2")
	defer release()

	// A renews too late: its claim ends while its step is stalled.
	a := mustOpen(t, b, instanceOpts{id: "A", claimTTL: time.Second, renewEvery: time.Hour})
	defer closeEngine(t, a)

	id, err := a.Start(t.Context(), "z1", "three", []byte("z1"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("step s2 did not start")
	}

	bb := mustOpen(t, b, instanceOpts{id: "B", claimTTL: time.Second})
	defer closeEngine(t, bb)

	byB := waitState(t, bb, id, durable.StateSucceeded, 8*time.Second)
	release()
	time.Sleep(500 * time.Millisecond)

	final := waitState(t, bb, id, durable.StateSucceeded, 10*time.Second)
	s2 := count(t, b, "z1", "s2")
	ok := byB.State == durable.StateSucceeded && s2 == 1
	result(t, b, "stalled holder is fenced", ok, "taken over while A stalled: "+boolText(byB.State == durable.StateSucceeded)+
		", final "+string(final.State)+", s2 rows "+itoa(s2))
}

func caseSchedule(t *testing.T, b *backing) {
	sched := []durable.Schedule{{Name: "tick", Kind: "tick", Every: time.Second}}

	a := mustOpen(t, b, instanceOpts{id: "A", sched: sched})
	bb := mustOpen(t, b, instanceOpts{id: "B", sched: sched})

	start := time.Now()

	time.Sleep(6 * time.Second)
	closeEngine(t, a)
	closeEngine(t, bb)

	elapsed := int(time.Since(start).Seconds())
	n := count(t, b, "tick", "tick")
	ok := n >= elapsed-3 && n <= elapsed+1
	result(t, b, "schedule fires once per tick", ok, "runs "+itoa(n)+" in "+itoa(elapsed)+" s with 2 instances")
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + intToString(n))
}

func intToString(n int) string {
	return strconvItoa(n)
}

func boolText(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}

func errText(errs ...error) string {
	var parts []string

	for _, e := range errs {
		if e != nil {
			parts = append(parts, e.Error())
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return ", errors: " + strings.Join(parts, "; ")
}

// skipDBOSSQLiteStall skips a case that opens a second DBOS instance while a
// step of the first one stalls inside its transaction. On SQLite the stalled
// step holds the one write lock, and DBOS Launch retries its first write
// with no limit, so the open never returns (measured: a 30 min test timeout).
func skipDBOSSQLiteStall(t *testing.T, b *backing) {
	t.Helper()

	if b.adapter == "dbos" && b.store == "sqlite" {
		t.Logf("RESULT | dbos | sqlite | %s | N/A | DBOS Launch blocks while another instance holds the SQLite write lock", t.Name())
		t.Skip("DBOS Launch blocks on the SQLite write lock of a stalled step")
	}
}
