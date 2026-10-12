package workspace_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// revealState returns a state file with a sensitive input holding the
// secret, in the shape the CLI writes.
func revealState() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id","input":"s3cr3t"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]}]}`
}

// revealMarks keeps the kept json-state marks of the reveal state non-empty,
// so the read masks the sensitive input only instead of every value.
func revealMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{"other.none": {"/id"}}
}

// TestRevealValueAnswersPlain proves reveal of a masked path answers the
// plain value.
func TestRevealValueAnswersPlain(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if len(masked) == 0 {
		t.Fatal("masked holds no path")
	}

	value, _, err := workspace.RevealValue([]byte(revealState()), revealMarks(), masked[0])
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "s3cr3t" {
		t.Fatalf("value = %#v, want s3cr3t", value)
	}
}

// TestRevealValueRejectsPlainPath proves a path outside the masked set
// answers a not-found error, so a caller cannot probe paths.
func TestRevealValueRejectsPlainPath(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	plain := "/resources/0/instances/0/attributes/id"
	for _, path := range masked {
		if path == plain {
			t.Fatalf("masked holds the plain path %q", plain)
		}
	}

	if _, _, err := workspace.RevealValue([]byte(revealState()), revealMarks(), plain); err == nil {
		t.Fatal("plain path succeeded, want a not-found error")
	} else if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Fatalf("plain path error = %v, want a not-found error", err)
	}
}

// TestRevealValueRejectsClientEncrypted proves a state the client encrypted
// fails with the client-encrypted sentinel.
func TestRevealValueRejectsClientEncrypted(t *testing.T) {
	t.Parallel()

	encrypted := `{"version":4,"encrypted_data":{"iv":"QUJD","data":"R0hJ"},"outputs":{},"resources":[]}`

	if _, _, err := workspace.RevealValue([]byte(encrypted), nil, "/resources/0"); err == nil {
		t.Fatal("encrypted state succeeded, want a client-encrypted error")
	} else if !errors.Is(err, workspace.ErrClientEncrypted) {
		t.Fatalf("encrypted error = %v, want a client-encrypted error", err)
	}
}
