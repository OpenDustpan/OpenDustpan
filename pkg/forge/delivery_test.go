package forge_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// write and remove are changes of the tests of the digest.
func write(path, content string) forge.FileChange {
	return forge.FileChange{Path: path, Content: []byte(content), Delete: false}
}

func remove(path string) forge.FileChange {
	return forge.FileChange{Path: path, Content: nil, Delete: true}
}

// TestChangesDigestKnownAnswer pins the form of the digest. The vectors come
// from an independent Python run, not from this code.
func TestChangesDigestKnownAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		changes []forge.FileChange
		want    string
	}{
		{"no change", nil, "sha256:56535ca6668d08cfc2a112f0f96a7e5927a06ee72ed5def8570a18021175ad22"},
		{
			"one file",
			[]forge.FileChange{write("a.txt", "a")},
			"sha256:fba8bd48f831c77a61fa10a332619d49c19e1541d2f36fe3fb2e96c4546e4905",
		},
		{
			"a write and a delete",
			[]forge.FileChange{write("stacks/prod.yaml", "a: 1\n"), remove("old.txt")},
			"sha256:66dc97011d29a14ec8c61f0558535653891a2940d81947e5d84ad3cf9080ce80",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := forge.ChangesDigest(tt.changes); got != tt.want {
				t.Fatalf("ChangesDigest = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestChangesDigestSeparatesChanges sends pairs of change sets that differ
// only in where one part ends and the next starts, or in one detail, straight
// to the digest. No pair may give one digest.
func TestChangesDigestSeparatesChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b []forge.FileChange
	}{
		{
			"path and content split",
			[]forge.FileChange{write("a.txt", "5:x")},
			[]forge.FileChange{write("a.txt5:", "x")},
		},
		{"path with a zero byte", []forge.FileChange{write("a\x00b", "")}, []forge.FileChange{write("a", "\x00b")}},
		{"a delete and an empty write", []forge.FileChange{remove("a.txt")}, []forge.FileChange{write("a.txt", "")}},
		{"the content of a delete", []forge.FileChange{remove("a.txt")}, []forge.FileChange{write("a.txt", "delete")}},
		{
			"the order of the changes",
			[]forge.FileChange{write("a.txt", "1"), write("b.txt", "2")},
			[]forge.FileChange{write("b.txt", "2"), write("a.txt", "1")},
		},
		{
			"one change or two",
			[]forge.FileChange{write("a.txt", "1"), write("b.txt", "2")},
			[]forge.FileChange{write("a.txt", "15:write1:2")},
		},
		{"other content", []forge.FileChange{write("a.txt", "a")}, []forge.FileChange{write("a.txt", "b")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if forge.ChangesDigest(tt.a) == forge.ChangesDigest(tt.b) {
				t.Fatal("two different change sets have one digest")
			}
		})
	}
}

// TestDeliveryOf checks that the delivery of a request holds its trailer,
// base, branch and the digest of its changes, and that it passes the check.
func TestDeliveryOf(t *testing.T) {
	t.Parallel()

	req := validCommit()
	d := forge.DeliveryOf(req)

	want := forge.Delivery{
		Trailer: req.Trailer, Base: req.Base, Branch: req.Branch, Changes: forge.ChangesDigest(req.Changes),
	}
	if d != want {
		t.Fatalf("DeliveryOf = %+v, want %+v", d, want)
	}

	if err := forge.ValidateDelivery(d); err != nil {
		t.Fatalf("ValidateDelivery of the delivery of a valid request = %v", err)
	}
}

// TestValidateDeliveryRefuses sends deliveries with one part that is not
// valid straight to the check. Each must fail with ErrInvalid.
func TestValidateDeliveryRefuses(t *testing.T) {
	t.Parallel()

	good := forge.DeliveryOf(validCommit())
	hex64 := strings.Repeat("a", 64)

	tests := []struct {
		name   string
		change func(d *forge.Delivery)
	}{
		{"no trailer", func(d *forge.Delivery) { d.Trailer = "" }},
		{"a short base", func(d *forge.Delivery) { d.Base = "0123456789abcdef" }},
		{"no branch", func(d *forge.Delivery) { d.Branch = "" }},
		{"a full ref as branch", func(d *forge.Delivery) { d.Branch = "refs/heads/main" }},
		{"a branch with ..", func(d *forge.Delivery) { d.Branch = "a..b" }},
		{"a branch with a zero byte", func(d *forge.Delivery) { d.Branch = "a\x00b" }},
		{"no changes digest", func(d *forge.Delivery) { d.Changes = "" }},
		{"a digest with no prefix", func(d *forge.Delivery) { d.Changes = hex64 }},
		{"a digest of another hash", func(d *forge.Delivery) { d.Changes = "sha1:" + hex64[:40] }},
		{"a short digest", func(d *forge.Delivery) { d.Changes = "sha256:" + hex64[:63] }},
		{"a long digest", func(d *forge.Delivery) { d.Changes = "sha256:" + hex64 + "a" }},
		{"a digest in upper case", func(d *forge.Delivery) { d.Changes = "sha256:" + strings.ToUpper(hex64) }},
		{"a digest with letters after f", func(d *forge.Delivery) { d.Changes = "sha256:" + strings.Repeat("g", 64) }},
		{"a digest with a z", func(d *forge.Delivery) { d.Changes = "sha256:" + hex64[:63] + "z" }},
		{"a digest with a zero byte", func(d *forge.Delivery) { d.Changes = "sha256:" + hex64[:63] + "\x00" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := good
			tt.change(&d)

			if err := forge.ValidateDelivery(d); !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("ValidateDelivery = %v, want ErrInvalid", err)
			}
		})
	}
}
