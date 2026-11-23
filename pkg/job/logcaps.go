package job

import (
	"fmt"
	"io"
)

// LogCaps bounds the log of one session: the chunks and the bytes of one step
// and of all the steps of one phase. LogCaps.PlanChunk refuses a new chunk that
// passes one of the caps with a LogCapError. A caller passes a positive value
// for each field; a cap that is not positive fails closed with ErrInvalid.
type LogCaps struct {
	ChunksPerStep  int
	BytesPerStep   int64
	ChunksPerPhase int
	BytesPerPhase  int64
}

// CappedChunk is the chunk Seq of the log of the step Step, with the caps of the
// log of its session. CheckedRecorder.AppendLogCapped stores it.
type CappedChunk struct {
	Step string
	Seq  int
	Body io.Reader
	Caps LogCaps
}

// LogCap names one cap of LogCaps.
type LogCap string

// The caps of the log.
const (
	LogCapStepChunks  LogCap = "step-chunks"
	LogCapStepBytes   LogCap = "step-bytes"
	LogCapPhaseChunks LogCap = "phase-chunks"
	LogCapPhaseBytes  LogCap = "phase-bytes"
)

// LogCapError is the refusal of a new chunk that passes the cap Cap, whose value
// is Limit. errors.Is reports it as ErrInvalid.
type LogCapError struct {
	Cap   LogCap
	Limit int64
	Phase int
}

// Error implements error.
func (e *LogCapError) Error() string {
	return fmt.Sprintf("%v: the log of phase %d passes the cap %s of %d", ErrInvalid, e.Phase, e.Cap, e.Limit)
}

// Is reports a LogCapError as ErrInvalid.
func (*LogCapError) Is(target error) bool {
	return target == ErrInvalid
}

// ChunkRule names a rule of the order and the content of the chunks of a log.
type ChunkRule string

// The rules of the chunks of a log.
const (
	// ChunkOutOfOrder refuses a new chunk that is not the next one.
	ChunkOutOfOrder ChunkRule = "out-of-order"
	// ChunkHashMismatch refuses a chunk with another hash for a stored number.
	ChunkHashMismatch ChunkRule = "hash-mismatch"
	// ChunkNameOrNumberInvalid refuses a chunk whose step name is not the name
	// of a step or whose number is not 0 to MaxLogChunks-1.
	ChunkNameOrNumberInvalid ChunkRule = "name-or-number-invalid"
)

// ChunkError is the refusal of a chunk by the rule Rule. Next is the number of
// the next chunk of the log for ChunkOutOfOrder, and 0 for the other rules.
// errors.Is reports ChunkHashMismatch as ErrConflict and the other rules as
// ErrInvalid.
type ChunkError struct {
	Rule ChunkRule
	Next int
	err  error
}

// CheckChunkKey returns a ChunkError of the rule ChunkNameOrNumberInvalid when
// step is not the name of a step or seq is not 0 to MaxLogChunks-1. The error
// text never holds the name.
func CheckChunkKey(step string, seq int) error {
	if checkStepName(step) != nil {
		return &ChunkError{
			Rule: ChunkNameOrNumberInvalid, Next: 0,
			err: fmt.Errorf("%w: the step name is 1 to %d lowercase letters, digits and '-'", ErrInvalid,
				MaxStepName),
		}
	}

	if seq < 0 || seq >= MaxLogChunks {
		return &ChunkError{
			Rule: ChunkNameOrNumberInvalid, Next: 0,
			err: fmt.Errorf("%w: chunk number %d is not 0 to %d", ErrInvalid, seq,
				MaxLogChunks-1),
		}
	}

	return nil
}

// Error implements error.
func (e *ChunkError) Error() string {
	return e.err.Error()
}

// Unwrap returns the error of the rule, which wraps ErrInvalid or ErrConflict.
func (e *ChunkError) Unwrap() error {
	return e.err
}

// PlanChunk is the function PlanChunk with the caps of the log of a session. A
// new chunk that passes a cap is a LogCapError, and a repeated chunk counts no
// second time. Nil caps set no cap: the call is the function PlanChunk. The
// store of a recorder calls it with ChunkReserve.Caps, in the transaction that
// reserves the chunk.
func (caps *LogCaps) PlanChunk(
	j *Job, k StepKey, c ChunkReserve, newLog *LogRef,
	lookup func(version string, seq int) (ChunkRow, bool, error),
) (ChunkPlan, error) {
	plan, err := PlanChunk(j, k, c, newLog, lookup)
	if err != nil || caps == nil {
		return plan, err
	}

	log, err := openLogOf(j, k)
	if err != nil {
		return ChunkPlan{}, err
	}

	var step logCount

	if log != nil {
		if !plan.Insert {
			return plan, nil
		}

		step = logCount{chunks: log.Chunks, bytes: log.Size}
	}

	if capErr := caps.check(k.Phase, c.Size, step, logTotals(j, k.Phase)); capErr != nil {
		return ChunkPlan{}, capErr
	}

	return plan, nil
}

// logCount is the number of chunks and of bytes of one log or of the logs of
// one phase.
type logCount struct {
	chunks int
	bytes  int64
}

// logTotals returns the chunks and the bytes of the logs of the steps of the
// phase at index phase of j. A step with no log counts nothing.
func logTotals(j *Job, phase int) logCount {
	var total logCount

	if phase < 0 || phase >= len(j.Phases) {
		return total
	}

	for i := range j.Phases[phase].Steps {
		if l := j.Phases[phase].Steps[i].Log; l != nil {
			total.chunks += l.Chunks
			total.bytes += l.Size
		}
	}

	return total
}

// check returns a LogCapError when a new chunk of size bytes passes a cap. step
// is the count of the log of the step before the chunk, and phase the count of
// the logs of the phase with the index at before the chunk.
func (caps *LogCaps) check(at int, size int64, step, phase logCount) error {
	refuse := func(c LogCap, limit int64) error {
		return &LogCapError{Cap: c, Limit: limit, Phase: at}
	}

	switch {
	case caps.ChunksPerStep <= 0 || caps.BytesPerStep <= 0 || caps.ChunksPerPhase <= 0 ||
		caps.BytesPerPhase <= 0:
		return fmt.Errorf("%w: the caps of the log are not positive", ErrInvalid)
	case step.chunks >= caps.ChunksPerStep:
		return refuse(LogCapStepChunks, int64(caps.ChunksPerStep))
	case step.bytes+size > caps.BytesPerStep:
		return refuse(LogCapStepBytes, caps.BytesPerStep)
	case phase.chunks >= caps.ChunksPerPhase:
		return refuse(LogCapPhaseChunks, int64(caps.ChunksPerPhase))
	case phase.bytes+size > caps.BytesPerPhase:
		return refuse(LogCapPhaseBytes, caps.BytesPerPhase)
	default:
		return nil
	}
}
