//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestServeReloadsOnSIGHUP proves the reloader of serve takes SIGHUP from
// the process: the signal reloads the file at once.
//
//nolint:paralleltest // The test sends SIGHUP to its own process; no other test may stop the handler meanwhile.
func TestServeReloadsOnSIGHUP(t *testing.T) {
	fx := newReloadFixture(t, `{"installation": "eu-west"}`, nil)

	stop := fx.r.flags.startReloader(t.Context(), fx.store, fx.results.observe, fx.r.logger)
	defer stop()

	fx.write(t, `{"installation": "us-east"}`)

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	waitFor(t, func() bool { return len(fx.store.appended()) == 1 })
}

// TestCatchHangupQueuesTheSignal proves serve catches SIGHUP as soon as it
// knows the configuration file: the signal waits in the channel and does
// not stop the process (the review of ADR 63 part 2, item 8). Without
// --config, nothing is caught.
//
//nolint:paralleltest // The test sends SIGHUP to its own process; no other test may stop the handler meanwhile.
func TestCatchHangupQueuesTheSignal(t *testing.T) {
	var none serveFlags

	none.catchHangup()()

	if none.hup != nil {
		t.Fatal("catchHangup without --config made a channel")
	}

	var f serveFlags

	f.config = "/etc/opendustpan/config.yaml"

	stop := f.catchHangup()
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	select {
	case <-f.hup:
	case <-time.After(time.Second):
		t.Fatal("SIGHUP did not reach the channel")
	}
}
