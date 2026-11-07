package castest

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
)

// Parts of a DNS message that the resolver of the tests reads.
const (
	dnsTypeA    = 1
	dnsTypeAAAA = 28
	dnsHeader   = 12
	// dnsMaxUDP is the largest DNS message over UDP.
	dnsMaxUDP = 512
	// dnsNamePointer marks a compressed name that points to an offset.
	dnsNamePointer = 0xC0
)

// Resolver starts a DNS server on loopback that answers each name with the
// addresses in answers, and returns a resolver that asks only that server.
// A test uses it to give a host name of a CAS URL the address that the test
// needs. The test cleanup stops the server.
func Resolver(t *testing.T, answers []netip.Addr) *net.Resolver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		serveDNS(pc, answers)
	}()

	t.Cleanup(func() {
		if cerr := pc.Close(); cerr != nil {
			t.Errorf("close the DNS server: %v", cerr)
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
	buf := make([]byte, dnsMaxUDP)

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
			rdata = append(rdata, record{data: b[:], size: net.IPv4len})
			count++
		case qtype == dnsTypeAAAA && a.Is6():
			b := a.As16()
			rdata = append(rdata, record{data: b[:], size: net.IPv6len})
			count++
		default:
		}
	}

	resp := append([]byte{}, q[:end]...)
	resp[2], resp[3] = 0x81, 0x80 // a response, recursion available, no error
	binary.BigEndian.PutUint16(resp[6:], count)
	resp[8], resp[9], resp[10], resp[11] = 0, 0, 0, 0

	for _, r := range rdata {
		resp = append(resp, dnsNamePointer, dnsHeader) // a pointer to the name of the question
		resp = binary.BigEndian.AppendUint16(resp, qtype)
		resp = binary.BigEndian.AppendUint16(resp, 1) // class IN
		resp = binary.BigEndian.AppendUint32(resp, 0) // time to live
		resp = binary.BigEndian.AppendUint16(resp, r.size)
		resp = append(resp, r.data...)
	}

	return resp
}
