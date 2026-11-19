//go:build race

package inventory_test

// raceDetector is true when the race detector is on. The detector makes each
// store call several times slower, so the latency target does not apply.
const raceDetector = true
