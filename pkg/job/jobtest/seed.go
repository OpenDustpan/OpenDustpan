package jobtest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ArtifactSeed names what SeedArtifacts stored: the namespace that holds it,
// the stored plan, and the log chunks that it appended.
type ArtifactSeed struct {
	Namespace namespace.Name
	Plan      job.PlanRef
	Chunks    int
}

// SeedArtifacts stores one plan and the chunks of one log through the recorder
// of h, as the server does: the content is sealed, and the store keeps the
// size and the MD5 of the sealed bytes. A backup suite uses it to fill a
// database and a blob store with the plan blobs and the log blobs that a
// backup must copy.
func SeedArtifacts(t *testing.T, h RecorderHarness) ArtifactSeed {
	t.Helper()

	f := setUp(t, h.Harness)

	plan := putPlan(t, h, openPlan(t, h, f, ""))

	a := openApply(t, h, f, "unit-0")
	mustBegin(t, h, a, stepPrepare)

	chunks := []string{"first chunk of the log\n", "second chunk of the log\n"}
	for seq, content := range chunks {
		mustAppend(t, h, a, seq, content)
	}

	return ArtifactSeed{Namespace: f.fence.Namespace, Plan: plan, Chunks: len(chunks)}
}
