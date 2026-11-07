package proxy_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
)

var errNotTCP = errors.New("the host connection is not a TCP connection")

// hostResult is what the raw host saw.
type hostResult struct {
	data string
	err  error
}

// serveHalfClose accepts one connection, reads n bytes, half-closes its write
// side, then reads until the client half-closes, and reports everything it
// read. It ends when the connection ends.
func serveHalfClose(l net.Listener, n int, done chan<- hostResult) {
	c, err := l.Accept()
	if err != nil {
		done <- hostResult{data: "", err: err}

		return
	}

	defer func() {
		if cerr := c.Close(); cerr != nil {
			return
		}
	}()

	head := make([]byte, n)
	if _, err = io.ReadFull(c, head); err != nil {
		done <- hostResult{data: "", err: err}

		return
	}

	tc, ok := c.(*net.TCPConn)
	if !ok {
		done <- hostResult{data: "", err: errNotTCP}

		return
	}

	if err = tc.CloseWrite(); err != nil {
		done <- hostResult{data: "", err: err}

		return
	}

	rest, err := io.ReadAll(c)
	done <- hostResult{data: string(head) + string(rest), err: err}
}

// TestTunnelHalfClose checks a tunnel whose client sends the CONNECT and the
// first payload in one write, so the proxy holds those bytes in the buffer of
// the hijack. The host half-closes its write side: the client must read EOF
// while the other direction still carries bytes to the host.
func TestTunnelHalfClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		first  string
		second string
	}{
		{name: "small payload", first: "ping", second: "more"},
		{name: "payload over one read", first: string(make([]byte, 6000)), second: string(make([]byte, 3000))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runHalfClose(t, tt.first, tt.second)
		})
	}
}

// runHalfClose runs one half-close scenario.
func runHalfClose(t *testing.T, first, second string) {
	t.Helper()

	port, done := startHalfCloseHost(t, len(first))
	e := newEnv(t, withRules(func(int) string { return tunnelRules(port) }))

	c := dialRaw(t, e.addr)
	bound(t, c, ioWait)

	br := connectWithPayload(t, c, testHost+":"+strconv.Itoa(port), first)

	// The host half-closed its write side, so the client must read EOF now,
	// before it has sent anything more.
	if rest, rerr := io.ReadAll(br); rerr != nil || len(rest) != 0 {
		t.Fatalf("client read %d bytes and error %v, want EOF at once", len(rest), rerr)
	}

	// The client to host direction still works, and a half-close of the
	// client ends it.
	send(t, c, second)

	tc, ok := c.(*net.TCPConn)
	if !ok {
		t.Fatal(errNotTCP)
	}

	if err := tc.CloseWrite(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ioWait)
	defer cancel()

	select {
	case res := <-done:
		if res.err != nil || res.data != first+second {
			t.Errorf("the host saw %d bytes and error %v, want %d bytes", len(res.data), res.err, len(first+second))
		}
	case <-ctx.Done():
		t.Fatal("the host did not see the end of the client stream")
	}
}

// startHalfCloseHost starts the raw host and returns its port and the channel
// for its result.
func startHalfCloseHost(t *testing.T, n int) (int, chan hostResult) {
	t.Helper()

	l := listenLoopback(t)
	closeAfter(t, l)

	_, p, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan hostResult, 1)

	go serveHalfClose(l, n, done)

	return port, done
}

// connectWithPayload sends the CONNECT and the payload in one write and reads
// the 200 answer.
func connectWithPayload(t *testing.T, c net.Conn, target, payload string) *bufio.Reader {
	t.Helper()

	send(t, c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"+payload)

	br := newReader(c)

	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("CONNECT: %v", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("close: %v", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status %d", resp.StatusCode)
	}

	return br
}
