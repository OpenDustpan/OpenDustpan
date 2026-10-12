// Package soak checks long runs for leaks and errors (ADR 40 decision 4).
//
// The e2e driver (TestSoak, behind the e2e and soak build tags) runs real
// Pulumi CLI and OpenTofu processes against one server binary for a set
// duration and samples the server every 10 seconds. This package holds the
// analysis behind it: the sample, the least-squares growth rule with its
// thresholds, the metrics text parser, and the CSV and summary writers.
//
// The growth rule fits a least-squares slope over the last two thirds of the
// samples and fails the run when a slope passes its threshold per minute.
// The first third is warm-up and never counts. The database connection
// series stays empty: the server exports no database pool gauge and neither
// store exposes sql.DBStats, so the verdict for that series is always "no
// data" and never fails the run.
//
// The driver reuses the e2e helpers for CLI installs, the binary server,
// TLS, tokens and namespaces, but not the load/ helpers: synthetic replay
// does not fit a real-CLI soak, which drives the CLIs themselves.
//
// docs/guides/soak.md shows the commands and the thresholds.
package soak
