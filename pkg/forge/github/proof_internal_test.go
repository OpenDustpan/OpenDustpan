package github

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// TestProofBindingSeparatesParts sends hostile parts straight to the binding,
// as if every check of the request and of the delivery had failed: two
// deliveries that move bytes from one part to the next must not give one
// binding.
func TestProofBindingSeparatesParts(t *testing.T) {
	t.Parallel()

	const (
		base    = "0123456789abcdef0123456789abcdef01234567"
		changes = "sha256:fba8bd48f831c77a61fa10a332619d49c19e1541d2f36fe3fb2e96c4546e4905"
	)

	tests := []struct {
		name         string
		repoA, repoB string
		a, b         forge.Delivery
	}{
		{
			name: "branch and changes, zero byte", repoA: "1", repoB: "1",
			a: forge.Delivery{Trailer: "T: 1", Base: base, Branch: "main\x00x", Changes: changes},
			b: forge.Delivery{Trailer: "T: 1", Base: base, Branch: "main", Changes: "x\x00" + changes},
		},
		{
			name: "trailer and base, length", repoA: "1", repoB: "1",
			a: forge.Delivery{Trailer: "T: 1" + "40:" + base, Base: "", Branch: "main", Changes: changes},
			b: forge.Delivery{Trailer: "T: 1", Base: base, Branch: "main", Changes: changes},
		},
		{
			name: "repository and trailer", repoA: "12", repoB: "1",
			a: forge.Delivery{Trailer: "T: 1", Base: base, Branch: "main", Changes: changes},
			b: forge.Delivery{Trailer: "2T: 1", Base: base, Branch: "main", Changes: changes},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if bytes.Equal(proofBinding(tt.repoA, tt.a), proofBinding(tt.repoB, tt.b)) {
				t.Fatal("two different deliveries have one binding")
			}
		})
	}
}
