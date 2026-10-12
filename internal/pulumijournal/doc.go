// Package pulumijournal reads and replays the journal entries of the Pulumi
// CLI.
//
// The package takes bytes and readers, and gives bytes, writers and errors.
// It imports no part of the server: no pkg/ package, no API adapter, no HTTP,
// no SQL. A depguard rule in .golangci.yml enforces this (ADR 43, decision 3).
package pulumijournal
