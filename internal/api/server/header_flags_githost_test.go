package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// TestGitHostHeaderFlagsMatchTheHandler proves that the header flags of the
// Git host operations match what the handler does with the headers. Each
// scenario starts on a fresh registration, assignment and connection.
func TestGitHostHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	scenarios := gitHostScenarios()

	for _, op := range newHostEnv(t, hostAllowAll()).handler.Operations() {
		if len(op.Tags) == 0 || op.Tags[0] != "git-host" {
			continue
		}

		scenario, ok := scenarios[op.ID]
		if !ok {
			t.Errorf("%s has no scenario", op.ID)

			continue
		}

		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			// The check route changes no value, so it refuses the key.
			headertest.Check(t, op, scenario, headertest.Options{RefusesKey: op.ID == "checkGitHostConnection"})
		})
	}
}

// gitHostScenarios returns one scenario for each Git host operation.
func gitHostScenarios() map[string]headertest.Scenario {
	const (
		app      = hostAppPath
		installs = hostInstallationsPath
		conns    = hostConnectionsPath
		install  = hostInstallationsPath + "/101"
	)

	// probe builds a probe on a fresh environment. etagOf is the path whose
	// answer holds the ETag that the probe sends as If-Match.
	probe := func(method, target, body, etagOf string, prep ...func(*testing.T, *hostEnv)) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			e := newHostEnv(t, hostAllowAll())
			e.register(t)

			id := e.connect(t, "101")

			for _, p := range prep {
				p(t, e)
			}
			resolve := func(s string) string { return strings.ReplaceAll(s, "{id}", id) }

			etag := ""
			if etagOf != "" {
				etag = e.send(t, http.MethodGet, resolve(etagOf), "").header.Get("ETag")
			}

			return headertest.Probe{
				ETag: etag, Replayed: nil,
				Send: func(headers map[string]string) headertest.Answer {
					got := e.do(t, hostCall{headers: headers, method: method, target: resolve(target), body: body})

					return headertest.Answer{Status: got.status, Header: got.header, Body: got.body}
				},
			}
		}
	}

	conn := conns + "/{id}"

	return map[string]headertest.Scenario{
		"getGitHostApp":                probe(http.MethodGet, app, "", ""),
		"putGitHostApp":                probe(http.MethodPut, app, hostAppBody(), app),
		"deleteGitHostApp":             probe(http.MethodDelete, app, "", app, unassign),
		"listGitHostInstallations":     probe(http.MethodGet, installs, "", ""),
		"getGitHostInstallation":       probe(http.MethodGet, install, "", ""),
		"putGitHostInstallation":       probe(http.MethodPut, install, `{"namespace":"beta"}`, install),
		"deleteGitHostInstallation":    probe(http.MethodDelete, install, "", install),
		"listGitHostConnections":       probe(http.MethodGet, conns, "", ""),
		"createGitHostConnection":      probe(http.MethodPost, conns, createBody("102"), "", assign102),
		"getGitHostConnection":         probe(http.MethodGet, conn, "", ""),
		"deleteGitHostConnection":      probe(http.MethodDelete, conn, "", conn),
		"makeGitHostConnectionDefault": probe(http.MethodPost, conn+"/make-default", "", conn),
		"checkGitHostConnection":       probe(http.MethodPost, conn+"/check", "", ""),
	}
}

// unassign ends the assignment, so that the registration can go.
func unassign(t *testing.T, e *hostEnv) {
	t.Helper()

	wantHost(t, e.send(t, http.MethodDelete, hostInstallationsPath+"/101", ""), http.StatusNoContent)
}

// assign102 gives installation 102 to acme, for the connection that the probe makes.
func assign102(t *testing.T, e *hostEnv) {
	t.Helper()

	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/102", `{"namespace":"acme"}`), http.StatusCreated)
}
