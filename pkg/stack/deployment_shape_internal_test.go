package stack

// White-box tests: checkDeploymentShape and innerDeploymentBytes have no
// exported caller without a store, so the fuzz test and the benchmark live in
// the internal test file, as the lint rules allow for _internal_test.go.

import (
	"encoding/json"
	"strings"
	"testing"
)

// readableDeployment reports whether the CLI can read deployment by the rules
// of the shape check. It decodes the whole value with encoding/json, so it
// does not share code with the streamed scan of checkDeploymentShape. As in
// the CLI, a known member name matches in any case.
func readableDeployment(deployment []byte) bool {
	var members map[string]json.RawMessage
	if json.Unmarshal(deployment, &members) != nil || members == nil {
		return false
	}

	for name, raw := range members {
		if !readableMember(name, raw) {
			return false
		}
	}

	return true
}

// readableMember reports whether raw is a value that the CLI can read for the
// member name of a deployment.
func readableMember(name string, raw json.RawMessage) bool {
	switch {
	case strings.EqualFold(name, "manifest"), strings.EqualFold(name, secretsProvidersMember):
		return raw[0] == '{' || raw[0] == 'n'
	case strings.EqualFold(name, "pending_operations"):
		return readableEntries(raw)
	case strings.EqualFold(name, "resources"):
		return readableResources(raw)
	default:
		return true
	}
}

// readableResources reports whether raw is null or an array of objects that
// each have a urn member that is a string. The name urn matches in any case,
// and each member with that name must be a string.
func readableResources(raw json.RawMessage) bool {
	if raw[0] == 'n' {
		return true
	}

	var entries []map[string]json.RawMessage
	if raw[0] != '[' || json.Unmarshal(raw, &entries) != nil {
		return false
	}

	for _, entry := range entries {
		if !readableURN(entry) {
			return false
		}
	}

	return true
}

// readableURN reports whether entry has a member urn, in any case, and
// whether each such member is a string.
func readableURN(entry map[string]json.RawMessage) bool {
	found := false

	for name, raw := range entry {
		if !strings.EqualFold(name, "urn") {
			continue
		}

		if raw[0] != '"' {
			return false
		}

		found = true
	}

	return found
}

// readableEntries reports whether raw is null or an array of objects.
func readableEntries(raw json.RawMessage) bool {
	if raw[0] == 'n' {
		return true
	}

	var entries []json.RawMessage
	if raw[0] != '[' || json.Unmarshal(raw, &entries) != nil {
		return false
	}

	for _, entry := range entries {
		if entry[0] != '{' {
			return false
		}
	}

	return true
}

// FuzzDeploymentShape proves that the shape check accepts a save only when the
// deployment that the reads serve for it is one that the CLI can read. For a
// sequenced save, the reads serve the inner deployment that
// innerDeploymentBytes takes from the envelope, and the check reads the
// envelope and that deployment in one pass.
func FuzzDeploymentShape(f *testing.F) {
	for _, seed := range []string{
		`{"resources":[]}`,
		`{"resources":[1]}`,
		`{"version":3,"deployment":{"resources":[]}}`,
		`{"version":3,"deployment":{"resources":[1]}}`,
		`{"version":3,"Deployment":{"resources":[{}]}}`,
		`{"version":3,"deployment":{"resources":[]},"deployment":null}`,
		`{"version":3,"deployment":{"resources":[1]},"deployment":{"resources":[]}}`,
		`{"version":3,"deployment":{"manifest":[]}}`,
		`{"version":3}`,
		`{"version":3,"deployment":"x"}`,
		`{"resources":[{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"}]}`,
		`{"resources":[{"urn":1}]}`,
		`{"version":3,"deployment":{"resources":[{"type":"x","urn":"u"},{}]}}`,
		`{"RESOURCES":5}`,
		`{"resources":[{"URN":"u"}]}`,
		`{"resources":[{"urn":"u","URN":5}]}`,
	} {
		f.Add([]byte(seed), int64(0))
		f.Add([]byte(seed), int64(1))
	}

	f.Fuzz(func(t *testing.T, deployment []byte, seq int64) {
		if checkDeploymentShape(deployment, seq) != nil {
			return
		}

		if inner := innerDeploymentBytes(deployment, seq); !readableDeployment(inner) {
			t.Fatalf("check accepts %q (sequence %d), but the reads serve %q", deployment, seq, inner)
		}
	})
}

// benchShape runs the shape check of a deployment of about size bytes per
// iteration. A sequence above 0 wraps it in the versioned envelope of a
// sequenced save, as each delta result is.
func benchShape(b *testing.B, size int, seq int64) {
	b.Helper()

	body := deltaApplyBenchBody(b, size)
	if seq > 0 {
		body = append(append([]byte(`{"version":3,"deployment":`), body...), '}')
	}

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if err := checkDeploymentShape(body, seq); err != nil {
			b.Fatalf("check the shape: %v", err)
		}
	}
}

// BenchmarkDeploymentShape1M checks a 1 MB whole save per iteration.
func BenchmarkDeploymentShape1M(b *testing.B) {
	benchShape(b, 1048576, 0)
}

// BenchmarkDeploymentShapeSequenced1M checks a 1 MB sequenced save per
// iteration: the shape of a delta result.
func BenchmarkDeploymentShapeSequenced1M(b *testing.B) {
	benchShape(b, 1048576, 1)
}

// BenchmarkDeploymentShapeSequenced4M checks a 4 MB sequenced save per
// iteration: the large load harness checkpoint.
func BenchmarkDeploymentShapeSequenced4M(b *testing.B) {
	benchShape(b, 4194304, 1)
}
