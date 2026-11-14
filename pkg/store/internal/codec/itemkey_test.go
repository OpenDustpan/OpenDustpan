package codec_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// itemKeyRow returns a row that a store wrote, with the attempt part attempt.
func itemKeyRow(attempt string) codec.ItemKeyRow {
	return codec.ItemKeyRow{
		ID: "ik-abc", Namespace: "acme", ScopeID: "unit-1", Kind: "plan", Item: "pln-1", WrapperID: "builtin-1",
		CreatedAtMs: 1_700_000_000_000, ShreddedAtMs: 0, BlobVersion: 1, BlobSize: 60,
		BlobMd5: make([]byte, 16), BlobAttempt: attempt,
	}
}

// TestFromItemKeyRowChecksTheAttempt sends the read path of the codec a row
// whose attempt part the column check would refuse, as if a database of
// another version or a restore had skipped that check. The codec refuses it,
// so a record never names a blob key of another path.
func TestFromItemKeyRowChecksTheAttempt(t *testing.T) {
	t.Parallel()

	if k, err := codec.FromItemKeyRow(itemKeyRow("abcdefghij0123456789")); err != nil ||
		k.Blob.Attempt != "abcdefghij0123456789" {
		t.Fatalf("valid row = %+v, %v", k.Blob, err)
	}

	for name, attempt := range map[string]string{
		"empty":            "",
		"short":            "abc",
		"long":             "abcdefghij01234567890",
		"upper case":       "ABCDEFGHIJ0123456789",
		"a slash":          "aaaaaaaaaaaaaaaaa/x1",
		"a parent segment": "aaaaaaaaaaaaaaaaa..1",
		"a line end":       "aaaaaaaaaaaaaaaaaa\n1",
	} {
		if _, err := codec.FromItemKeyRow(itemKeyRow(attempt)); !errors.Is(err, secret.ErrInvalidItemRef) {
			t.Errorf("row with an attempt part that is %s err = %v, want ErrInvalidItemRef", name, err)
		}
	}
}
