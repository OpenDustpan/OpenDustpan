package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// IPAllowlistInvalidCIDR means that an IP allowlist holds an entry that is
	// not a valid CIDR range, or holds too many entries.
	IPAllowlistInvalidCIDR = "ip-allowlist.invalid-cidr"
)

// ipAllowlistSpecs are the codes of the IP allowlist.
func ipAllowlistSpecs() []Spec {
	return []Spec{
		{
			Code:    IPAllowlistInvalidCIDR,
			Meaning: "an IP allowlist holds an entry that is not a valid CIDR range, or too many entries",
			Args:    nil,
		},
	}
}
