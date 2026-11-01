package main_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// fakeServerEnv names the variable that turns this test binary into a fake
// `opendustpan serve` process. Its value is the mode.
const fakeServerEnv = "LOAD_FAKE_SERVER"

// runFakeServer plays a server for the tests of the process code. It reads
// --listen, binds, logs the address as the real server does, and answers
// /health/ready with 200. The mode changes the behavior:
//
//	ready      stops on SIGTERM
//	ignoreterm ignores SIGTERM, so only SIGKILL stops it
//	exit       ends with status 3 before it listens
//	silent     listens but never logs the address
func runFakeServer() int {
	mode := os.Getenv(fakeServerEnv)

	if mode == "exit" {
		return fakeExitStatus
	}

	var config net.ListenConfig

	listener, err := config.Listen(context.Background(), "tcp", fakeListenAddr())
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)

		return 2
	}

	if mode != "silent" {
		if err := announce(listener); err != nil {
			return 2
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}

	if mode == "ignoreterm" {
		signal.Ignore(syscall.SIGTERM)
	}

	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM)

	go func() {
		<-stopped

		if mode != "ignoreterm" {
			if err := server.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "close:", err)
			}
		}
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "serve:", err)

		return 2
	}

	return 0
}

// fakeExitStatus is the exit status of the mode "exit".
const fakeExitStatus = 3

// fakeListenAddr answers the value of --listen, or a free loopback port.
func fakeListenAddr() string {
	for i, arg := range os.Args {
		if arg == "--listen" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}

	return "127.0.0.1:0"
}

// announce writes the listening line that the real server writes.
func announce(listener net.Listener) error {
	_, err := fmt.Fprintf(os.Stdout, "{\"msg\":\"listening\",\"addr\":%q}\n", listener.Addr().String())
	if err != nil {
		return fmt.Errorf("announce: %w", err)
	}

	return nil
}
