package ipallow_test

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/ipallow"
)

// parseCase is one ParseList case: items parse to want, or fail naming
// wantErr.
type parseCase struct {
	name    string
	items   []string
	want    []string
	wantErr string
}

// parseCases holds the valid and invalid items, masking, bare addresses,
// duplicates and mapped items.
func parseCases() []parseCase {
	return []parseCase{
		{name: "cidr", items: []string{"10.0.0.0/8"}, want: []string{"10.0.0.0/8"}, wantErr: ""},
		{name: "masked", items: []string{"10.1.2.3/8"}, want: []string{"10.0.0.0/8"}, wantErr: ""},
		{name: "bare ipv4", items: []string{"192.0.2.1"}, want: []string{"192.0.2.1/32"}, wantErr: ""},
		{name: "bare ipv6", items: []string{"2001:db8::1"}, want: []string{"2001:db8::1/128"}, wantErr: ""},
		{name: "ipv6 cidr", items: []string{"2001:db8::/32"}, want: []string{"2001:db8::/32"}, wantErr: ""},
		{
			name:    "mapped item is unmapped",
			items:   []string{"::ffff:10.1.2.3/104"},
			want:    []string{"10.0.0.0/8"},
			wantErr: "",
		},
		{
			name:    "duplicates are dropped, order kept",
			items:   []string{"10.0.0.0/8", "2001:db8::/32", "10.1.0.0/16", "10.0.0.0/8"},
			want:    []string{"10.0.0.0/8", "2001:db8::/32", "10.1.0.0/16"},
			wantErr: "",
		},
		{name: "empty list gives the zero list", items: nil, want: nil, wantErr: ""},
		{name: "not an address", items: []string{"nope"}, want: nil, wantErr: `"nope"`},
		{name: "empty item", items: []string{""}, want: nil, wantErr: `""`},
		{name: "bad prefix length", items: []string{"10.0.0.0/33"}, want: nil, wantErr: `"10.0.0.0/33"`},
	}
}

// TestParseList checks valid items, masking, bare addresses, duplicates,
// mapped items and invalid items.
func TestParseList(t *testing.T) {
	t.Parallel()

	for _, tc := range parseCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkParseCase(t, tc)
		})
	}
}

// checkParseCase runs one ParseList case.
func checkParseCase(t *testing.T, tc parseCase) {
	t.Helper()

	got, err := ipallow.ParseList(tc.items)
	if tc.wantErr != "" {
		checkParseError(t, tc, err)

		return
	}

	if err != nil {
		t.Fatalf("parse %q: %v", tc.items, err)
	}

	if !slices.Equal(got.Strings(), tc.want) {
		t.Fatalf("parse %q = %v, want %v", tc.items, got.Strings(), tc.want)
	}

	if tc.items == nil && !got.IsEmpty() {
		t.Fatalf("parse nil = %v, want the zero list", got.Strings())
	}
}

// checkParseError checks a ParseList failure wraps ErrInvalidList and names
// the item.
func checkParseError(t *testing.T, tc parseCase, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("parse %q: err = nil, want an error", tc.items)
	}

	if !errors.Is(err, ipallow.ErrInvalidList) {
		t.Fatalf("parse %q: err = %v, want ErrInvalidList", tc.items, err)
	}

	if !strings.Contains(err.Error(), tc.wantErr) {
		t.Fatalf("parse %q: err = %v, want it to name %s", tc.items, err, tc.wantErr)
	}
}

// distinctItems returns n distinct /24 CIDRs under 10.0.0.0/8.
func distinctItems(n int) []string {
	items := make([]string, 0, n)
	for i := range n {
		items = append(items, fmt.Sprintf("10.0.%d.0/24", i))
	}

	return items
}

// TestParseListMaxEntries checks 64 distinct prefixes parse and 65 fail.
func TestParseListMaxEntries(t *testing.T) {
	t.Parallel()

	got, err := ipallow.ParseList(distinctItems(64))
	if err != nil {
		t.Fatalf("parse 64 addresses: %v", err)
	}

	if len(got.Strings()) != 64 {
		t.Fatalf("parse 64 addresses = %d entries, want 64", len(got.Strings()))
	}

	_, err = ipallow.ParseList(distinctItems(65))
	if !errors.Is(err, ipallow.ErrInvalidList) {
		t.Fatalf("parse 65 addresses: err = %v, want ErrInvalidList", err)
	}
}

// TestContains checks Contains for IPv4, IPv6, a mapped IPv4 address in a v4
// list and the zero address.
func TestContains(t *testing.T) {
	t.Parallel()

	v4, err := ipallow.ParseList([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("parse v4 list: %v", err)
	}

	v6, err := ipallow.ParseList([]string{"2001:db8::/32"})
	if err != nil {
		t.Fatalf("parse v6 list: %v", err)
	}

	cases := []struct {
		name string
		list ipallow.List
		addr string
		want bool
	}{
		{name: "ipv4 in the list", list: v4, addr: "10.1.2.3", want: true},
		{name: "ipv4 outside the list", list: v4, addr: "192.0.2.1", want: false},
		{name: "ipv6 in the list", list: v6, addr: "2001:db8::1", want: true},
		{name: "ipv6 outside the list", list: v6, addr: "2001:db9::1", want: false},
		{name: "mapped ipv4 in a v4 list", list: v4, addr: "::ffff:10.1.2.3", want: true},
		{name: "ipv4 never in a v6 list", list: v6, addr: "10.1.2.3", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr, parseErr := netip.ParseAddr(tc.addr)
			if parseErr != nil {
				t.Fatalf("parse address %q: %v", tc.addr, parseErr)
			}

			if got := tc.list.Contains(addr); got != tc.want {
				t.Fatalf("contains %q = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// TestContainsZeroAddr checks the zero address is never contained.
func TestContainsZeroAddr(t *testing.T) {
	t.Parallel()

	l, err := ipallow.ParseList([]string{"0.0.0.0/0", "::/0"})
	if err != nil {
		t.Fatalf("parse open list: %v", err)
	}

	if l.Contains(netip.Addr{}) {
		t.Fatal("an open list contains the zero address, want false")
	}
}

// TestContainsOpenRanges checks the open ranges match one family only:
// 0.0.0.0/0 matches IPv4 clients and never IPv6 clients, ::/0 matches IPv6
// clients and never IPv4 clients, and a mapped client address reads as its
// IPv4 address first.
func TestContainsOpenRanges(t *testing.T) {
	t.Parallel()

	v4open, err := ipallow.ParseList([]string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("parse v4 open list: %v", err)
	}

	v6open, err := ipallow.ParseList([]string{"::/0"})
	if err != nil {
		t.Fatalf("parse v6 open list: %v", err)
	}

	cases := []struct {
		name string
		list ipallow.List
		addr string
		want bool
	}{
		{name: "v4 client in v4 open", list: v4open, addr: "192.0.2.1", want: true},
		{name: "v6 client outside v4 open", list: v4open, addr: "2001:db8::1", want: false},
		{name: "v6 client in v6 open", list: v6open, addr: "2001:db8::1", want: true},
		{name: "v4 client outside v6 open", list: v6open, addr: "192.0.2.1", want: false},
		{name: "mapped client in v4 open", list: v4open, addr: "::ffff:192.0.2.1", want: true},
		{name: "mapped client outside v6 open", list: v6open, addr: "::ffff:192.0.2.1", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr, parseErr := netip.ParseAddr(tc.addr)
			if parseErr != nil {
				t.Fatalf("parse address %q: %v", tc.addr, parseErr)
			}

			if got := tc.list.Contains(addr); got != tc.want {
				t.Fatalf("contains %q = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// TestEncodeDecodeRoundTrip checks Encode and DecodeList invert each other,
// including the empty list.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	l, err := ipallow.ParseList([]string{"10.0.0.0/8", "2001:db8::/32", "192.0.2.1"})
	if err != nil {
		t.Fatalf("parse list: %v", err)
	}

	decoded, err := ipallow.DecodeList(l.Encode())
	if err != nil {
		t.Fatalf("decode %q: %v", l.Encode(), err)
	}

	if !slices.Equal(decoded.Strings(), l.Strings()) {
		t.Fatalf("round trip = %v, want %v", decoded.Strings(), l.Strings())
	}

	var zero ipallow.List

	if zero.Encode() != "" {
		t.Fatalf("zero list encodes as %q, want empty", zero.Encode())
	}

	empty, err := ipallow.DecodeList("")
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}

	if !empty.IsEmpty() {
		t.Fatalf("decode empty = %v, want the zero list", empty.Strings())
	}
}

// TestDecodeListInvalid checks a bad stored value wraps ErrInvalidList.
func TestDecodeListInvalid(t *testing.T) {
	t.Parallel()

	_, err := ipallow.DecodeList("10.0.0.0/8,nope")
	if !errors.Is(err, ipallow.ErrInvalidList) {
		t.Fatalf("decode a bad value: err = %v, want ErrInvalidList", err)
	}
}
