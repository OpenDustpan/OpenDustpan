package clientip_test

import (
	"context"
	"net/netip"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
)

func TestFromContext(t *testing.T) {
	t.Parallel()

	want := netip.MustParseAddr("192.0.2.10")
	ctx := clientip.WithAddr(context.Background(), want)

	got := clientip.FromContext(ctx)
	if got != want {
		t.Fatalf("FromContext = %v, want %v", got, want)
	}
}

func TestFromContextBareGivesZero(t *testing.T) {
	t.Parallel()

	got := clientip.FromContext(context.Background())
	if got.IsValid() {
		t.Fatalf("FromContext of a bare context = %v, want the zero Addr", got)
	}
}

func TestFromContextStoredZeroGivesZero(t *testing.T) {
	t.Parallel()

	ctx := clientip.WithAddr(context.Background(), netip.Addr{})

	got := clientip.FromContext(ctx)
	if got.IsValid() {
		t.Fatalf("FromContext of a stored zero Addr = %v, want the zero Addr", got)
	}
}
