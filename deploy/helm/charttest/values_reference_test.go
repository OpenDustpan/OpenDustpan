package charttest_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	valuesFile    = "../opendustpan/values.yaml"
	referencePage = "../../../docs/reference/helm-values.md"
)

// TestValuesReference fails when the reference page does not name a key of
// values.yaml. It needs no helm.
func TestValuesReference(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(valuesFile)
	if err != nil {
		t.Fatalf("read %s: %v", valuesFile, err)
	}

	var values map[string]any
	if err = yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("parse %s: %v", valuesFile, err)
	}

	page, err := os.ReadFile(referencePage)
	if err != nil {
		t.Fatalf("read %s: %v", referencePage, err)
	}

	keys := leafKeys("", values)
	if len(keys) == 0 {
		t.Fatalf("%s has no keys", valuesFile)
	}

	var missing []string

	for _, key := range keys {
		if !strings.Contains(string(page), "`"+key+"`") {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		t.Errorf("%s does not name these keys of values.yaml: %s", referencePage, strings.Join(missing, ", "))
	}
}

// leafKeys returns the dotted path of each leaf of m. A leaf is a scalar, a
// list, an empty map, or the map under a matchLabels key, whose keys are
// label names and not chart keys.
func leafKeys(prefix string, m map[string]any) []string {
	var keys []string

	for name, v := range m {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}

		sub, ok := v.(map[string]any)
		if !ok || len(sub) == 0 || name == "matchLabels" {
			keys = append(keys, path)

			continue
		}

		keys = append(keys, leafKeys(path, sub)...)
	}

	slices.Sort(keys)

	return keys
}
