package job

import (
	"strconv"
	"testing"
)

// TestSweepResultTruncatesItsKeys shows that a run lists at most MaxSweptKeys
// keys, counts every delete, and sets Truncated, also when the results of
// several jobs merge.
func TestSweepResultTruncatesItsKeys(t *testing.T) {
	t.Parallel()

	var one ArtifactSweepResult
	for i := range MaxSweptKeys + 5 {
		one.add("k" + strconv.Itoa(i))
	}

	if one.Deleted != MaxSweptKeys+5 || len(one.DeletedKeys) != MaxSweptKeys || !one.Truncated {
		t.Fatalf("one job = %d deleted, %d keys, truncated %v", one.Deleted, len(one.DeletedKeys), one.Truncated)
	}

	var run ArtifactSweepResult

	run.add("first")
	run.merge(&one)

	if run.Deleted != MaxSweptKeys+6 || len(run.DeletedKeys) != MaxSweptKeys || !run.Truncated {
		t.Fatalf("run = %d deleted, %d keys, truncated %v", run.Deleted, len(run.DeletedKeys), run.Truncated)
	}

	var small ArtifactSweepResult

	small.add("a")

	if small.Truncated || len(small.DeletedKeys) != 1 {
		t.Fatalf("small run = %+v, want one key and no truncation", small)
	}
}
