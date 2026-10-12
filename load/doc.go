// Command load replays recorded CLI request sequences against a server it
// starts itself, and writes a machine-readable JSON report for the nightly
// budget job (ADR 40 decision 3).
//
// The sequences mirror the shapes e2e/recorder.go records from the real
// CLIs: the Pulumi update cycle (create stack, create and start update, full
// and delta checkpoint saves, complete), stack import, the OpenTofu lock and
// state upload cycle, the workspace list, and the inventory list. The
// harness needs no real CLI at run time. Small recorded fixtures live under
// testdata; large bodies are generated, so the report stays free of secrets.
//
// Usage:
//
//	make load
//	go run ./load -out load-report.json
//	DUSTPAN_TEST_POSTGRES_URL=postgres://... go run ./load -out pg-report.json
//
// With SQLite (the default) the harness runs one instance. With
// DUSTPAN_TEST_POSTGRES_URL set it opens the URL twice and runs two
// instances against it, so the report covers several instances. Point the
// URL at an empty database; the store migrates it on open.
//
// docs/guides/load.md names each JSON field.
package main
