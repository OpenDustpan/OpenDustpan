package dustpan_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// errHook is the error of a failing before-serve function.
var errHook = errors.New("hook failed")

// TestBeforeServeRunsBeforeServing proves Run calls the function of
// WithBeforeServe once, after the listen and before the server answers.
func TestBeforeServeRunsBeforeServing(t *testing.T) {
	t.Parallel()

	ln := openTestListener(t)
	addr := ln.Addr().String()

	var calls atomic.Int32

	hook := dustpan.WithBeforeServe(func(context.Context) error {
		calls.Add(1)

		return nil
	})

	srv := newServerWithOptions(t, "127.0.0.1:0", dustpan.WithListener(ln), hook)

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, srv)

	waitForTCP(t, addr)

	// The test holds the listener, so the port accepts before Run starts.
	// Stop only after the function ran: a Run that stops while it starts up
	// returns before the function.
	waitUntil(t, func() bool { return calls.Load() == 1 })
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("the function ran %d times, want 1", got)
	}
}

// waitUntil polls cond every 10 ms until it holds, and fails after 10 seconds.
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)

	defer ticker.Stop()

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not hold in time")
		}

		<-ticker.C
	}
}

// TestBeforeServeErrorStopsTheStart proves a failed function stops Run with
// its error and closes the listener, so the server never answers.
func TestBeforeServeErrorStopsTheStart(t *testing.T) {
	t.Parallel()

	ln := openTestListener(t)
	addr := ln.Addr().String()

	hook := dustpan.WithBeforeServe(func(context.Context) error { return errHook })
	srv := newServerWithOptions(t, "127.0.0.1:0", dustpan.WithListener(ln), hook)

	err := srv.Run(t.Context())
	if !errors.Is(err, errHook) {
		t.Fatalf("run = %v, want %v", err, errHook)
	}

	//nolint:exhaustruct_v5 // The probe dials with defaults only.
	conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if dialErr == nil {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}

		t.Fatal("the listener still accepts after a failed function")
	}
}

// TestBeforeServeSkippedWhenAStartCheckFails proves Run does not call the
// function when a start-up check fails, so a failed start writes no start
// entry (ADR 63 decision 10).
func TestBeforeServeSkippedWhenAStartCheckFails(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	hook := dustpan.WithBeforeServe(func(context.Context) error {
		calls.Add(1)

		return nil
	})

	srv := newRunCheckServer(t, seedRunCheckStore(t), hook)

	if err := srv.Run(t.Context()); err == nil {
		t.Fatal("run with another key answered no error")
	}

	if got := calls.Load(); got != 0 {
		t.Fatalf("the function ran %d times after a failed check, want 0", got)
	}
}
