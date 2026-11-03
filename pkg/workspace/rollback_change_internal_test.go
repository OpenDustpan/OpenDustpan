package workspace

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
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
// read, a state above it fails with ErrTooManyResources, and the work at the
// limit stays linear (a quadratic compare of 100,000 addresses takes minutes).
func TestRollbackChangeLimit(t *testing.T) {
	t.Parallel()

	atLimit := instancesDoc(MaxRollbackInstances)
	above := instancesDoc(MaxRollbackInstances + 1)

	start := time.Now()

	got, err := rollbackChangeOf([]byte(instancesDoc(1)), "sv-next", []byte(atLimit), "sv-cur")
	if err != nil || got.Resources != 1 || len(got.Removed) != MaxRollbackInstances-1 {
		t.Fatalf("at the limit: change with %d removed, err %v", len(got.Removed), err)
	}

	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("the read of a state at the limit took %s, want a linear compare", took)
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

// TestIsEncryptionEnvelope sends look-alikes of the encryption envelope
// straight to the check, as if the layers above had not read the state.
func TestIsEncryptionEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		want    bool
		wantErr bool
	}{
		{"plaintext state", `{"version":4,"resources":[]}`, false, false},
		{"the envelope", `{"meta":{},"encrypted_data":"x","encryption_version":"v0"}`, true, false},
		{
			"the envelope with serial and lineage",
			`{"serial":3,"lineage":"l","meta":{"k":"v"},"encrypted_data":"x","encryption_version":"v0"}`, true, false,
		},
		{
			"a plaintext state with the member",
			`{"encrypted_data":"x","resources":[{"type":"t","name":"a"}]}`,
			false,
			true,
		},
		{"the member alone", `{"encrypted_data":"x"}`, false, true},
		{"no meta", `{"encrypted_data":"x","encryption_version":"v0"}`, false, true},
		{"no version", `{"meta":{},"encrypted_data":"x"}`, false, true},
		{"meta is a list", `{"meta":[],"encrypted_data":"x","encryption_version":"v0"}`, false, true},
		{"data is an object", `{"meta":{},"encrypted_data":{"iv":"a"},"encryption_version":"v0"}`, false, true},
		{
			"a serial that is text",
			`{"serial":"3","meta":{},"encrypted_data":"x","encryption_version":"v0"}`,
			false,
			true,
		},
		{
			"a lineage that is a number",
			`{"lineage":3,"meta":{},"encrypted_data":"x","encryption_version":"v0"}`,
			false,
			true,
		},
		{"not an object", `[]`, false, true},
		{"not JSON", `nope`, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := isEncryptionEnvelope([]byte(tc.doc), "sv-1")
			if got != tc.want || (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, ErrInvalidState)) {
				t.Fatalf("isEncryptionEnvelope() = %v, %v, want %v and error %v", got, err, tc.want, tc.wantErr)
			}
		})
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
