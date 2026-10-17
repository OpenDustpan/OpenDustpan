//go:build e2e && journalrecording

package e2e_test

// journalingBuildAnswersNonzero reports whether this build answers a nonzero
// journal version with journaling on. The recording build answers 1, so the
// on-run check requires a nonzero answer or accepted journal batches.
func journalingBuildAnswersNonzero() bool {
	return true
}
