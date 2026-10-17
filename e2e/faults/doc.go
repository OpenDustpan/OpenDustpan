//go:build faults

// Package faults runs Toxiproxy network fault tests against a self-started server.
//
// The tests drive Toxiproxy only through its HTTP API with net/http.
// Toxiproxy is a test-only tool and never ships in the server binary.
//
// The package is excluded from `make test` by the faults build tag; run it with `make faults`.
package faults
