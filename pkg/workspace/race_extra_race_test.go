//go:build race

package workspace_test

// raceHeapExtra covers the heap the race detector and delayed collection add
// to the sealed outputs peak (35 to 40 MiB, as the old guard noted).
const raceHeapExtra = 16 << 20
