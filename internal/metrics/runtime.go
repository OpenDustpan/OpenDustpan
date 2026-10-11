package metrics

import (
	runtimemetrics "runtime/metrics"
)

// Runtime values from runtime/metrics for callback gauges. The package does
// not register them itself. The caller registers them with RegisterFunc.
// Issue #162 owns that step.

// Goroutines returns the current goroutine count.
func Goroutines() float64 {
	return readUintMetric("/sched/goroutines:goroutines")
}

// HeapBytes returns the live heap size in bytes.
func HeapBytes() float64 {
	return readUintMetric("/memory/classes/heap/objects:bytes")
}

// GCCycles returns the completed garbage collection cycles, automatic and
// manual.
func GCCycles() float64 {
	return readUintMetric("/gc/cycles/automatic:gc-cycles") + readUintMetric("/gc/cycles/manual:gc-cycles")
}

// readUintMetric reads one uint64 runtime metric by name. It returns zero
// when the runtime does not know the name.
func readUintMetric(name string) float64 {
	var samples [1]runtimemetrics.Sample
	samples[0].Name = name
	runtimemetrics.Read(samples[:])
	if samples[0].Value.Kind() != runtimemetrics.KindUint64 {
		return 0
	}

	return float64(samples[0].Value.Uint64())
}
