package egresshttp_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
)

const (
	dnsTypeA    = 1
	dnsTypeAAAA = 28
	dnsHeader   = 12
)

// fakeResolver starts a DNS server on loopback that answers every name with
// the addresses in answers, and returns a resolver that asks only that
// server.
func fakeResolver(t *testing.T, answers []netip.Addr) *net.Resolver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		serveDNS(pc, answers)
	}()

	t.Cleanup(func() {
		if cerr := pc.Close(); cerr != nil {
			t.Error(cerr)
		}

		<-done
	})

	server := pc.LocalAddr().String()

	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: false,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, "udp", server)
		},
	}
}

// serveDNS answers each query with the addresses of the asked record type.
func serveDNS(pc net.PacketConn, answers []netip.Addr) {
	buf := make([]byte, 512)

	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		resp := dnsAnswer(buf[:n], answers)
		if resp == nil {
			continue
		}

		if _, err = pc.WriteTo(resp, from); err != nil {
			return
		}
	}
}

// dnsAnswer builds the response to one query, or nil when the query is not
// well formed.
func dnsAnswer(q []byte, answers []netip.Addr) []byte {
	if len(q) < dnsHeader+5 {
		return nil
	}

	end := dnsHeader

	for end < len(q) && q[end] != 0 {
		end += int(q[end]) + 1
	}

	end += 5 // the root label, the type and the class
	if end > len(q) {
		return nil
	}

	qtype := binary.BigEndian.Uint16(q[end-4:])

	type record struct {
		data []byte
		size uint16
	}

	var (
		rdata []record
		count uint16
	)

	for _, a := range answers {
		switch {
		case qtype == dnsTypeA && a.Is4():
			b := a.As4()
			rdata = append(rdata, record{b[:], net.IPv4len})
			count++
		case qtype == dnsTypeAAAA && a.Is6():
			b := a.As16()
			rdata = append(rdata, record{b[:], net.IPv6len})
			count++
		default:
			continue
		}
	}

	resp := append([]byte{}, q[:end]...)
	resp[2], resp[3] = 0x81, 0x80 // a response, recursion available, no error
	binary.BigEndian.PutUint16(resp[6:], count)
	resp[8], resp[9], resp[10], resp[11] = 0, 0, 0, 0

	for _, r := range rdata {
		resp = append(resp, 0xC0, dnsHeader) // a pointer to the name of the question
		resp = binary.BigEndian.AppendUint16(resp, qtype)
		resp = binary.BigEndian.AppendUint16(resp, 1) // class IN
		resp = binary.BigEndian.AppendUint32(resp, 0) // time to live
		resp = binary.BigEndian.AppendUint16(resp, r.size)
		resp = append(resp, r.data...)
	}

	return resp
}

// countingListener listens on loopback and counts the connections that it
// accepts. A request that passes the dial guard connects to it.
func countingListener(t *testing.T) (string, *atomic.Int64) {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	accepts := new(atomic.Int64)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}

			accepts.Add(1)

			if cerr := c.Close(); cerr != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		if cerr := ln.Close(); cerr != nil {
			t.Error(cerr)
		}

		<-done
	})

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	return port, accepts
}

// TestResolvedAddressRefused checks that the dial guard refuses a host name
// whose DNS answer holds an address that the rules refuse, before any
// connection opens. The URL check passes these URLs: the host is a name.
// The listener on loopback is the target of a rebinding attack: it must
// accept no connection.
func TestResolvedAddressRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		answers []string
	}{
		{"loopback v4", []string{"127.0.0.1"}},
		{"loopback v6", []string{"::1"}},
		{"private 10/8", []string{"10.0.0.5"}},
		{"private 172.16/12", []string{"172.16.0.5"}},
		{"private 192.168/16", []string{"192.168.1.5"}},
		{"unique local v6", []string{"fd00::5"}},
		{"link-local v4", []string{"169.254.1.1"}},
		{"link-local v6", []string{"fe80::1"}},
		{"cloud metadata", []string{"169.254.169.254"}},
		{"shared address space", []string{"100.64.0.1"}},
		{"reserved 240/4", []string{"240.0.0.1"}},
		{"unspecified", []string{"0.0.0.0"}},
		{"v4-mapped loopback", []string{"::ffff:127.0.0.1"}},
		{"several private answers", []string{"10.0.0.5", "127.0.0.1", "169.254.169.254"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			answers := make([]netip.Addr, len(tt.answers))
			for i, s := range tt.answers {
				answers[i] = netip.MustParseAddr(s)
			}

			if !hasIPv6(t, answers) {
				t.Skip("the host has no IPv6 support: the socket fails before the dial guard runs")
			}

			err := dialHost(t, answers, 10*time.Second)
			if !errors.Is(err, egress.ErrRefused) || !errors.Is(err, egresshttp.ErrDialRefused) {
				t.Fatalf("Do = %v, want ErrDialRefused and ErrRefused", err)
			}
		})
	}
}

// hasIPv6 reports whether the host can open the IPv6 sockets that answers
// need. The dial guard runs after the socket opens, so a host without IPv6
// fails earlier with another error.
func hasIPv6(t *testing.T, answers []netip.Addr) bool {
	t.Helper()

	for _, a := range answers {
		if !a.Is6() || a.Is4In6() {
			continue
		}

		var lc net.ListenConfig

		pc, err := lc.ListenPacket(t.Context(), "udp6", "[::1]:0")
		if err != nil {
			return false
		}

		if cerr := pc.Close(); cerr != nil {
			t.Error(cerr)
		}

		return true
	}

	return true
}

// TestResolvedAddressAllowedConnects is the positive control of the refusal
// tests: with the loopback rule of the test, the same name and the answer
// 127.0.0.1 passes the dial guard, and the listener accepts exactly one
// connection. It shows that the zero-accept checks above can fail.
func TestResolvedAddressAllowedConnects(t *testing.T) {
	t.Parallel()

	port, accepts := countingListener(t)

	c := egresshttp.ClientWith(
		egress.Rules{Allow: nil, LoopbackForTests: true},
		egress.NoRedirect, egress.HTTPOrHTTPS,
		egresshttp.Options{RootCAs: nil, Resolver: fakeResolver(t, []netip.Addr{netip.MustParseAddr("127.0.0.1")})},
	)
	t.Cleanup(c.CloseIdleConnections)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://rebind.example.test:"+port+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	// The listener closes the connection without an answer, so the request
	// fails after the connection opens. The failure must not be a refusal.
	resp, err := c.Do(req)
	if err == nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Error(cerr)
		}
	} else if errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Do = %v, want no refusal", err)
	}

	if n := accepts.Load(); n != 1 {
		t.Fatalf("the listener accepted %d connections, want 1", n)
	}
}

// dialHost sends a request to a host name that the fake resolver
// answers with answers, and checks that the request fails and that the
// listener on loopback accepts no connection. It returns the error of the
// request.
func dialHost(t *testing.T, answers []netip.Addr, timeout time.Duration) error {
	t.Helper()

	port, accepts := countingListener(t)

	c := egresshttp.ClientWith(
		egress.Rules{Allow: nil, LoopbackForTests: false},
		egress.NoRedirect, egress.HTTPOrHTTPS,
		egresshttp.Options{RootCAs: nil, Resolver: fakeResolver(t, answers)},
	)
	t.Cleanup(c.CloseIdleConnections)

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://rebind.example.test:"+port+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Do(req)
	if err == nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Error(cerr)
		}

		t.Fatal("Do = nil error, want a refusal")
	}

	if n := accepts.Load(); n != 0 {
		t.Fatalf("the listener accepted %d connections, want 0", n)
	}

	return fmt.Errorf("do: %w", err)
}
