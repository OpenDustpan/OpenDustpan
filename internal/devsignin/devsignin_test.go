package devsignin_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestOptionNeedsMaker proves that Option refuses to answer before a maker is
// registered, and answers what the maker answers after.
func TestOptionNeedsMaker(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct_v5 // The test reads the subject only.
	cfg := devsignin.Config{Code: nil, Admin: identity.Principal{Subject: "admin"}}

	if _, err := devsignin.Option(cfg); !errors.Is(err, devsignin.ErrNotRegistered) {
		t.Fatalf("Option before Register: err = %v, want ErrNotRegistered", err)
	}

	devsignin.Register(func(c devsignin.Config) any { return c.Admin.Subject })

	got, err := devsignin.Option(cfg)
	if err != nil || got != "admin" {
		t.Fatalf("Option = %v, %v; want admin", got, err)
	}
}
