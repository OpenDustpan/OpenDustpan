package workspace

import (
	"runtime/debug"
	"strconv"
	"testing"
)

// TestSecretIndexMemoPinsDigestedContainers proves that the memo of the index
// keeps each digested container alive. A container that the index digested and
// that the document then drops must not free its address: a new container of
// the same length at that address would read the old digest, and a real
// duplicate of a secret would answer as no duplicate, so it would stay
// unmasked.
func TestSecretIndexMemoPinsDigestedContainers(t *testing.T) {
	t.Parallel()

	work := newMaskWork(nil)
	secret := map[string]any{"s": "x"}
	index := newSecretIndex(work, []any{secret})

	const rounds = 300

	for round := range rounds {
		// Digest a subtree, then replace the child, as the duplicate pass
		// replaces a child with a marker. The dropped child is garbage unless
		// the memo pins it.
		parent := map[string]any{"child": map[string]any{"d": "y" + strconv.Itoa(round)}}
		index.digest(parent)

		parent["child"] = "replaced"

		debug.FreeOSMemory() // forces a collection that frees the dropped containers

		// Allocate containers of the same length, which reuse a freed address.
		for i := range 64 {
			duplicate := map[string]any{"s": "x"}
			other := map[string]any{"s": "x" + strconv.Itoa(i)}

			if !index.duplicates(duplicate) {
				t.Fatalf("round %d: a real duplicate of the secret is not found", round)
			}

			if index.duplicates(other) {
				t.Fatalf("round %d: a value that differs from the secret reads as a duplicate", round)
			}
		}
	}
}

// TestSecretIndexDigestIsStableAfterGC proves the same pin at the digest: the
// digest of a container equals the digest of an equal copy, after the
// collector has freed other digested containers.
func TestSecretIndexDigestIsStableAfterGC(t *testing.T) {
	t.Parallel()

	work := newMaskWork(nil)
	index := newSecretIndex(work, nil)

	for round := range 200 {
		index.digest(map[string]any{"k": "v" + strconv.Itoa(round)})
		index.digest([]any{"a" + strconv.Itoa(round)})

		debug.FreeOSMemory() // forces a collection that frees the dropped containers

		value := map[string]any{"k": "w" + strconv.Itoa(round)}
		list := []any{"b" + strconv.Itoa(round)}
		copyOfValue := map[string]any{"k": "w" + strconv.Itoa(round)}
		copyOfList := []any{"b" + strconv.Itoa(round)}

		if index.digest(value) != index.digest(copyOfValue) {
			t.Fatalf("round %d: an equal object has another digest", round)
		}

		if index.digest(list) != index.digest(copyOfList) {
			t.Fatalf("round %d: an equal list has another digest", round)
		}
	}
}
