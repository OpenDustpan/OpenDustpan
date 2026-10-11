package main

// Health subcommand of the container image (issue #272): a client of GET
// /health/ready (ADR 34).
//
//	opendustpan health [--listen <addr>] [--tls-cert <file>]
//
// The command probes the local server and prints ready on a 2xx answer. It
// reads the same listen address and TLS settings as serve, so the
// HEALTHCHECK instruction of the Dockerfile and an operator use the same
// values: --listen (DUSTPAN_LISTEN) and --tls-cert (DUSTPAN_TLS_CERT). With
// --tls-cert set it probes over HTTPS, else over HTTP. The exit code is 0
// on a 2xx answer and 1 on each other result.

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	// healthRequestTimeout bounds one readiness probe. It stays below the
	// timeout of the HEALTHCHECK instruction, so the command answers
	// before Docker ends it.
	healthRequestTimeout = 5 * time.Second
	// healthBodyLimit caps the answer body the command reads for an error:
	// the readiness answer is small, and the limit keeps a long body out
	// of the error text.
	healthBodyLimit = 4 << 10
)

// Sentinel errors of the health subcommand. Each error text names what
// failed and the one command that helps.
var (
	// errHealthNotReady reports a probe that reached the server without a
	// 2xx answer, for example 503 while the database is down.
	errHealthNotReady = errors.New("server is not ready")
	// errHealthFailed reports a probe that never reached the server.
	errHealthFailed = errors.New("health check failed")
)

// healthFlags holds the settings of the health subcommand: the same
// listen address and TLS settings as serve, read from the same flags and
// variables, so the probe meets the server with or without TLS.
type healthFlags struct {
	listen  string
	tlsCert string
}

func (f *healthFlags) register(fs *flag.FlagSet) {
	fs.StringVar(
		&f.listen,
		"listen",
		env("DUSTPAN_LISTEN", ":8080"),
		"listen address of the server (DUSTPAN_LISTEN)",
	)
	fs.StringVar(
		&f.tlsCert,
		"tls-cert",
		env("DUSTPAN_TLS_CERT", ""),
		"same certificate file serve uses: when set, probe over HTTPS (DUSTPAN_TLS_CERT)",
	)
}

// runHealth runs `opendustpan health`: it sends GET /health/ready to the
// local server, prints ready on a 2xx answer, and returns an error on each
// other result, so the exit code is 0 only when the server is ready.
func runHealth(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var f healthFlags

	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return errUsage
	}

	url, err := healthURL(f.listen, f.tlsCert)
	if err != nil {
		return err
	}

	status, body, err := probeReady(ctx, newHealthClient(f.tlsCert), url)
	if err != nil {
		return err
	}

	if status < 200 || status > 299 {
		return fmt.Errorf(
			"%w: GET %s answered %d %s: check the server log and run opendustpan health again",
			errHealthNotReady,
			url,
			status,
			body,
		)
	}

	if _, err := fmt.Fprintln(stdout, "ready"); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// healthURL answers the readiness address for listen with the scheme that
// tlsCert selects: https when set, else http. An empty or wildcard host
// probes loopback, so a server that binds every interface still answers.
func healthURL(listen, tlsCert string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil || strings.TrimSpace(port) == "" {
		return "", fmt.Errorf(
			"%w: listen address %q: set --listen (DUSTPAN_LISTEN) to host:port and run opendustpan health again",
			errHealthFailed,
			listen,
		)
	}

	switch strings.TrimSpace(host) {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	default:
		host = strings.TrimSpace(host)
	}

	scheme := schemeHTTP
	if strings.TrimSpace(tlsCert) != "" {
		scheme = schemeHTTPS
	}

	return scheme + "://" + net.JoinHostPort(host, strings.TrimSpace(port)) + "/health/ready", nil
}

// newHealthClient answers the probe client: plain HTTP, or HTTPS when
// tlsCert is set, with the certificate check off. The probe dials the
// local server on loopback and sends no credential, so no other party can
// use a forged answer. The transport dials direct without a proxy: the
// probe always meets the local server, so no proxy can serve it.
func newHealthClient(tlsCert string) *http.Client {
	transport := &http.Transport{}

	if strings.TrimSpace(tlsCert) != "" {
		//nolint:gosec // The probe dials the local server on loopback and sends no credential.
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	}

	return &http.Client{Timeout: healthRequestTimeout, Transport: transport}
}

// probeReady sends one GET to url and answers the status with the trimmed
// body. A request error wraps errHealthFailed and names --listen, so the
// operator knows which setting to check.
func probeReady(ctx context.Context, client *http.Client, url string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, "", fmt.Errorf(
			"%w: GET %s: %w: check --listen (DUSTPAN_LISTEN) and run opendustpan health again",
			errHealthFailed,
			url,
			err,
		)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf(
			"%w: GET %s: %w: check --listen (DUSTPAN_LISTEN) and run opendustpan health again",
			errHealthFailed,
			url,
			err,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to its limit below.
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, healthBodyLimit))
	if err != nil {
		return 0, "", fmt.Errorf(
			"%w: GET %s: %w: check the server log and run opendustpan health again",
			errHealthFailed,
			url,
			err,
		)
	}

	return resp.StatusCode, strings.TrimSpace(string(raw)), nil
}
