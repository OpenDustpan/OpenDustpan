package durable

import "testing"

func TestCryptoFloat64InUnitInterval(t *testing.T) {
	t.Parallel()

	seen := map[bool]bool{}

	for range 1000 {
		f := cryptoFloat64()
		if f < 0 || f >= 1 {
			t.Fatalf("cryptoFloat64() = %v, want a number in [0, 1)", f)
		}

		seen[f < jitterMiddle] = true
	}

	if len(seen) != 2 {
		t.Fatal("1000 draws of cryptoFloat64 all fell in one half of [0, 1)")
	}
}
