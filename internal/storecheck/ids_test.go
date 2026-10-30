package storecheck_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/blob"
)

func TestParseIDAcceptsOnlyTheIDsOfTheService(t *testing.T) {
	t.Parallel()

	for range 20 {
		id := storecheck.NewID()

		if _, err := storecheck.ParseID(id.String()); err != nil {
			t.Fatalf("ParseID(%q) of a new ID: %v", id, err)
		}

		if _, err := blob.ParseKey(id.Prefix() + "x"); err != nil {
			t.Fatalf("the prefix of %q is not a valid key prefix: %v", id, err)
		}
	}

	tests := []string{
		"", "chk-", "chk", "CHK-abc", "chk-ABC", "chk-a/b", "chk-a.b", "chk-..", "chk-a b", "chk-a\nb", "bkp-abc",
		"chk-" + strings.Repeat("a", 40), "chk-é", "../chk-abc", "store-checks/chk-abc",
	}

	for _, s := range tests {
		if _, err := storecheck.ParseID(s); !errors.Is(err, storecheck.ErrInvalidID) {
			t.Errorf("ParseID(%q) err = %v, want ErrInvalidID", s, err)
		}
	}
}

func TestParseTargetAcceptsTheFixedNamesOnly(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"blobs", "backup"} {
		if got, err := storecheck.ParseTarget(s); err != nil || string(got) != s {
			t.Errorf("ParseTarget(%q) = %q, %v", s, got, err)
		}
	}

	for _, s := range []string{"", "Blobs", " blobs", "blobs ", "s3://bucket", "https://example.test", "file:///tmp", "../x", "all"} {
		if _, err := storecheck.ParseTarget(s); !errors.Is(err, storecheck.ErrInvalidTarget) {
			t.Errorf("ParseTarget(%q) err = %v, want ErrInvalidTarget", s, err)
		}
	}
}

func TestEveryKeyOfACheckIsExemptFromTheGarbageReaper(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	id := r.start(t, "blobs")
	r.runOne(t)

	puts := 0

	for _, call := range r.blobs.calls {
		verb, key, _ := strings.Cut(call, " ")
		if verb != "put" {
			continue
		}

		puts++

		if !blob.StoreCheckKey(blob.Key(key)) || !strings.HasPrefix(key, id.Prefix()) {
			t.Fatalf("key %q is not exempt from the garbage reaper", key)
		}
	}

	if puts == 0 {
		t.Fatal("the check wrote no object")
	}
}
