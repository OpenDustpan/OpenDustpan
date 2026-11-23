package jobtest

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// logCapCases returns the cases of the caps of the log of a session. The store
// checks ChunkReserve.Caps in the transaction that reserves the chunk, so a
// store that does not check them fails these cases.
func logCapCases() []recorderCase {
	return []recorderCase{
		{"log caps: a chunk above the step byte cap is ErrInvalid and keeps no byte", testLogCapStepBytes},
		{"log caps: a chunk above the phase chunk cap is ErrInvalid and makes no log", testLogCapPhaseChunks},
		{"log caps: the reserve of the store refuses a chunk above the phase byte cap", testLogCapReservePhaseBytes},
		{"log caps: caps that are not positive are ErrInvalid", testLogCapNotPositive},
	}
}

// wideCaps returns caps that a case does not reach.
func wideCaps() job.LogCaps {
	const many = 1000

	return job.LogCaps{ChunksPerStep: many, BytesPerStep: many, ChunksPerPhase: many, BytesPerPhase: many}
}

// appendCapped appends the chunk seq of the step with the caps.
func appendCapped(t *testing.T, h RecorderHarness, a *job.Assignment, at chunkOf, caps job.LogCaps) error {
	t.Helper()

	return h.Recorder.AppendLogCapped(t.Context(), a.Claim, job.CappedChunk{
		Step: at.step, Seq: at.seq, Body: strings.NewReader(at.content), Caps: caps,
	})
}

// chunkOf is the chunk seq of the log of the step, with its content.
type chunkOf struct {
	step    string
	seq     int
	content string
}

// wantCap fails t when err is not a LogCapError for the cap want.
func wantCap(t *testing.T, name string, err error, want job.LogCap) {
	t.Helper()

	var capErr *job.LogCapError
	if !errors.As(err, &capErr) || capErr.Cap != want || !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("%s = %v; want a LogCapError for the cap %s", name, err, want)
	}
}

func testLogCapStepBytes(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	caps := wideCaps()
	caps.BytesPerStep = 4

	if err := appendCapped(t, h, a, chunkOf{stepPrepare, 0, "abc"}, caps); err != nil {
		t.Fatalf("a chunk below the step byte cap = %v", err)
	}

	err := appendCapped(t, h, a, chunkOf{stepPrepare, 1, "xy"}, caps)
	wantCap(t, "a chunk above the step byte cap", err, job.LogCapStepBytes)

	if log := logOf(t, h, a); log == nil || log.Chunks != 1 || log.Size != 3 {
		t.Fatalf("log after the refusal = %+v; want 1 chunk of 3 bytes", log)
	}

	// A repeated chunk counts no second time, and the chunk that fills the cap
	// is stored.
	if err = appendCapped(t, h, a, chunkOf{stepPrepare, 0, "abc"}, caps); err != nil {
		t.Fatalf("a repeat of chunk 0 = %v", err)
	}

	if err = appendCapped(t, h, a, chunkOf{stepPrepare, 1, "x"}, caps); err != nil {
		t.Fatalf("a chunk at the step byte cap = %v", err)
	}

	if log := logOf(t, h, a); log == nil || log.Chunks != 2 || log.Size != 4 {
		t.Fatalf("log = %+v; want 2 chunks of 4 bytes", log)
	}
}

func testLogCapPhaseChunks(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	caps := wideCaps()
	caps.ChunksPerPhase = 2

	for seq := range 2 {
		if err := appendCapped(t, h, a, chunkOf{stepPrepare, seq, "x"}, caps); err != nil {
			t.Fatalf("chunk %d of the step prepare = %v", seq, err)
		}
	}

	mustEnd(t, h, a, stepPrepare, 0)
	mustBegin(t, h, a, stepApply)

	err := appendCapped(t, h, a, chunkOf{stepApply, 0, "y"}, caps)
	wantCap(t, "a chunk above the phase chunk cap", err, job.LogCapPhaseChunks)

	for _, r := range stepsOf(t, h, a) {
		if r.Name == stepApply && r.Log != nil {
			t.Fatalf("log of the step apply after the refusal = %+v; want none", r.Log)
		}
	}
}

func testLogCapReservePhaseBytes(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "abcd")

	caps := wideCaps()
	caps.BytesPerPhase = 5

	const content = "xy"

	c := job.ChunkReserve{Seq: 1, SHA256: hashOf(content), Size: int64(len(content)), Caps: &caps}

	_, err := h.Records.ReserveLogChunk(t.Context(), a.Claim, stepPrepare, c, nil)
	wantCap(t, "the reserve of a chunk above the phase byte cap", err, job.LogCapPhaseBytes)

	// The same reserve with no caps is stored: the refusal kept no row.
	c.Caps = nil
	if _, err = h.Records.ReserveLogChunk(t.Context(), a.Claim, stepPrepare, c, nil); err != nil {
		t.Fatalf("the reserve with no caps = %v", err)
	}
}

func testLogCapNotPositive(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)

	cases := []struct {
		name string
		edit func(c *job.LogCaps)
	}{
		{"no chunks for each step", func(c *job.LogCaps) { c.ChunksPerStep = 0 }},
		{"negative bytes for each step", func(c *job.LogCaps) { c.BytesPerStep = -1 }},
		{"no chunks for each phase", func(c *job.LogCaps) { c.ChunksPerPhase = 0 }},
		{"no bytes for each phase", func(c *job.LogCaps) { c.BytesPerPhase = 0 }},
	}

	for _, tc := range cases {
		caps := wideCaps()
		tc.edit(&caps)

		if err := appendCapped(t, h, a, chunkOf{stepPrepare, 0, "x"}, caps); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("%s: AppendLogCapped = %v; want ErrInvalid", tc.name, err)
		}
	}

	if log := logOf(t, h, a); log != nil {
		t.Fatalf("log after the refusals = %+v; want none", log)
	}
}
