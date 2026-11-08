package workspace

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// instancesDoc returns a state with one resource block web with n instances
// that have the index keys 0 to n-1.
func instancesDoc(n int) string {
	var b strings.Builder

	b.WriteString(`{"resources":[{"type":"aws_instance","name":"web","instances":[`)

	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `{"index_key":%d}`, i)
	}

	b.WriteString(`]}]}`)

	return b.String()
}

// TestRollbackChangeCountsInstances proves that the facts count resource
// instances, so the removal of web[1] to web[9] shows when web[0] stays.
func TestRollbackChangeCountsInstances(t *testing.T) {
	t.Parallel()

	got, err := rollbackChangeOf([]byte(instancesDoc(1)), "sv-next", []byte(instancesDoc(10)), "sv-cur")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"aws_instance.web[1]", "aws_instance.web[2]", "aws_instance.web[3]", "aws_instance.web[4]",
		"aws_instance.web[5]", "aws_instance.web[6]", "aws_instance.web[7]", "aws_instance.web[8]",
		"aws_instance.web[9]",
	}
	if got.Resources != 1 || !slices.Equal(got.Removed, want) || got.Current != "sv-cur" || got.Encrypted {
		t.Fatalf("change = %+v, want 1 resource and web[1] to web[9] removed", got)
	}
}

// TestRollbackChangeNamesKeysOfForEach proves a for_each key reads as a quoted
// key and a resource with no instance key reads as the plain address.
func TestRollbackChangeNamesKeysOfForEach(t *testing.T) {
	t.Parallel()

	cur := `{"resources":[{"type":"t","name":"a","instances":[{"index_key":"x"},{}]},{"type":"t","name":"b"}]}`
	next := `{"resources":[]}`

	got, err := rollbackChangeOf([]byte(next), "sv-next", []byte(cur), "sv-cur")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{`t.a`, `t.a["x"]`}; !slices.Equal(got.Removed, want) {
		t.Fatalf("removed = %q, want %q", got.Removed, want)
	}
}

// TestRollbackChangeLimit proves the instance limit: a state at the limit is
// read, and a state above it fails with ErrTooManyResources.
func TestRollbackChangeLimit(t *testing.T) {
	t.Parallel()

	atLimit := instancesDoc(MaxRollbackInstances)
	above := instancesDoc(MaxRollbackInstances + 1)

	got, err := rollbackChangeOf([]byte(instancesDoc(1)), "sv-next", []byte(atLimit), "sv-cur")
	if err != nil || got.Resources != 1 || len(got.Removed) != MaxRollbackInstances-1 {
		t.Fatalf("at the limit: change with %d removed, err %v", len(got.Removed), err)
	}

	for name, args := range map[string][2]string{
		"the restored state": {above, atLimit},
		"the current state":  {atLimit, above},
	} {
		_, err = rollbackChangeOf([]byte(args[0]), "sv-next", []byte(args[1]), "sv-cur")
		if !errors.Is(err, ErrTooManyResources) {
			t.Fatalf("%s above the limit: err = %v, want ErrTooManyResources", name, err)
		}
	}
}

// encryptionOutcome is the answer of the one check for one document: a plain
// state, the envelope, or a document the check cannot classify.
type encryptionOutcome int

const (
	// outcomePlain is a state that is not encrypted.
	outcomePlain encryptionOutcome = iota
	// outcomeEncrypted is the exact envelope of the client.
	outcomeEncrypted
	// outcomeError is a document the check cannot classify.
	outcomeError
)

// TestClientEncryptionPathsAgree proves that the service path and the rollback
// path give the same answer for each input of the one check: a refusal with
// the check's error, a state the client encrypted, or a plain state. The rows
// cover the inputs of both checks the server had: the envelope, the look-alikes
// that only the loose check read as encrypted, and the documents the strict
// check refused.
func TestClientEncryptionPathsAgree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want encryptionOutcome
	}{
		{
			"a plain state",
			`{"version":4,"serial":1,"lineage":"l1","outputs":{},"resources":[]}`, outcomePlain,
		},
		{"the envelope", `{"meta":{},"encrypted_data":"x","encryption_version":"v0"}`, outcomeEncrypted},
		{
			"the envelope with serial and lineage",
			`{"serial":3,"lineage":"l","meta":{"k":"v"},"encrypted_data":"x","encryption_version":"v0"}`,
			outcomeEncrypted,
		},
		{"the member alone", `{"encrypted_data":"x"}`, outcomeError},
		{
			"a plaintext state with the member",
			`{"encrypted_data":"x","resources":[{"type":"t","name":"a"}]}`, outcomeError,
		},
		{"an extra member", `{"meta":{},"encrypted_data":"x","encryption_version":"v0","a":1}`, outcomeError},
		{"an unknown version", `{"meta":{},"encrypted_data":"x","encryption_version":"v9"}`, outcomeError},
		{"a version of another kind", `{"meta":{},"encrypted_data":"x","encryption_version":0}`, outcomeError},
		{"no meta", `{"encrypted_data":"x","encryption_version":"v0"}`, outcomeError},
		{"no version", `{"meta":{},"encrypted_data":"x"}`, outcomeError},
		{"meta is a list", `{"meta":[],"encrypted_data":"x","encryption_version":"v0"}`, outcomeError},
		{"data is an object", `{"meta":{},"encrypted_data":{"iv":"a"},"encryption_version":"v0"}`, outcomeError},
		{
			"a serial that is text",
			`{"serial":"3","meta":{},"encrypted_data":"x","encryption_version":"v0"}`, outcomeError,
		},
		{
			"a lineage that is a number",
			`{"lineage":3,"meta":{},"encrypted_data":"x","encryption_version":"v0"}`, outcomeError,
		},
		{
			"an envelope in another object",
			`{"outer":{"meta":{},"encrypted_data":"x","encryption_version":"v0"}}`, outcomePlain,
		},
		{
			"an envelope as the value of encrypted_data",
			`{"meta":{},"encrypted_data":"{\"meta\":{},\"encrypted_data\":\"x\",\"encryption_version\":\"v0\"}",` +
				`"encryption_version":"v0"}`,
			outcomeEncrypted,
		},
		{"not an object", `[]`, outcomeError},
		{"null", `null`, outcomeError},
		{"not JSON", `nope`, outcomeError},
		{"empty input", ``, outcomeError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkClientEncrypted(t, tc.doc, tc.want)
			checkServiceRefusal(t, tc.doc, tc.want)
			checkRollbackRefusal(t, tc.doc, tc.want)
		})
	}
}

// checkClientEncrypted checks the answer of the one check.
func checkClientEncrypted(t *testing.T, doc string, want encryptionOutcome) {
	t.Helper()

	got, err := IsClientEncrypted([]byte(doc))

	switch want {
	case outcomeError:
		if got || err == nil || !errors.Is(err, ErrInvalidState) {
			t.Fatalf("IsClientEncrypted() = %v, %v, want the error of the check", got, err)
		}
	case outcomeEncrypted:
		if err != nil || !got {
			t.Fatalf("IsClientEncrypted() = %v, %v, want Encrypted", got, err)
		}
	case outcomePlain:
		if err != nil || got {
			t.Fatalf("IsClientEncrypted() = %v, %v, want a plain state", got, err)
		}
	default:
		t.Fatalf("unknown outcome %v", want)
	}
}

// checkServiceRefusal checks the refusal of the service path, the resource
// read.
func checkServiceRefusal(t *testing.T, doc string, want encryptionOutcome) {
	t.Helper()

	svcErr := rejectEncryptedState([]byte(doc), "sv-1")

	switch want {
	case outcomeError:
		if svcErr == nil || !errors.Is(svcErr, ErrInvalidState) {
			t.Fatalf("rejectEncryptedState() = %v, want the error of the check", svcErr)
		}
	case outcomeEncrypted:
		if !errors.Is(svcErr, ErrClientEncrypted) {
			t.Fatalf("rejectEncryptedState() = %v, want ErrClientEncrypted", svcErr)
		}
	case outcomePlain:
		if svcErr != nil {
			t.Fatalf("rejectEncryptedState() = %v, want nil", svcErr)
		}
	default:
		t.Fatalf("unknown outcome %v", want)
	}
}

// checkRollbackRefusal checks the refusal of the rollback path, with the state
// on either side of the change.
func checkRollbackRefusal(t *testing.T, doc string, want encryptionOutcome) {
	t.Helper()

	plain := `{"resources":[]}`

	for side, args := range map[string][2]string{
		"restored": {doc, plain},
		"current":  {plain, doc},
	} {
		change, err := rollbackChangeOf([]byte(args[0]), "sv-next", []byte(args[1]), "sv-cur")

		switch want {
		case outcomeError:
			if err == nil || !errors.Is(err, ErrInvalidState) {
				t.Fatalf("%s side: change = %+v, %v, want the error of the check", side, change, err)
			}
		case outcomeEncrypted:
			if err != nil || !change.Encrypted {
				t.Fatalf("%s side: change = %+v, %v, want Encrypted", side, change, err)
			}
		case outcomePlain:
			if err != nil || change.Encrypted {
				t.Fatalf("%s side: change = %+v, %v, want a plain state", side, change, err)
			}
		default:
			t.Fatalf("%s side: unknown outcome %v", side, want)
		}
	}
}

// TestRollbackChangeNeverReadsALookAlikeAsEncrypted proves the spoof of the
// review: a plaintext state with an encrypted_data member and resources fails
// with ErrInvalidState on either side and never gives Encrypted.
func TestRollbackChangeNeverReadsALookAlikeAsEncrypted(t *testing.T) {
	t.Parallel()

	spoof := `{"encrypted_data":"x","resources":[{"type":"aws_s3_bucket","name":"data"}]}`
	plain := `{"resources":[]}`

	for name, args := range map[string][2]string{"restored": {spoof, plain}, "current": {plain, spoof}} {
		got, err := rollbackChangeOf([]byte(args[0]), "sv-next", []byte(args[1]), "sv-cur")
		if !errors.Is(err, ErrInvalidState) || got.Encrypted {
			t.Fatalf("%s spoof: change %+v, err %v, want ErrInvalidState and no Encrypted", name, got, err)
		}
	}
}
