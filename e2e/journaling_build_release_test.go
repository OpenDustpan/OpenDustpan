//go:build e2e && !journalrecording

package e2e_test

// journalingBuildAnswersNonzero reports whether this build answers a nonzero
// journal version with journaling on. The release build answers 0 by design,
// so the on-run check proves the negotiation ran instead of a nonzero answer.
func journalingBuildAnswersNonzero() bool {
	return false
}
