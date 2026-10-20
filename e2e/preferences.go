//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TypedRequest is one request of ServerAPIStatusTyped. An empty Token sends no
// credential, and an empty ContentType sends no Content-Type.
type TypedRequest struct {
	Token       string
	Method      string
	Path        string
	ContentType string
	Body        string
}

// ServerAPIStatusTyped sends one installation API request with the Token as a
// Bearer credential and the ContentType as the Content-Type, and returns its
// status, headers and body. A step uses it for a route that needs a media type
// other than JSON, for example the merge patch of the preferences.
func (e *Env) ServerAPIStatusTyped(ctx context.Context, in TypedRequest) (APIResponse, error) {
	method, path := in.Method, in.Path

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, strings.NewReader(in.Body))
	if err != nil {
		return APIResponse{}, fmt.Errorf("build %s %s: %w", method, path, err)
	}

	if in.Token != "" {
		req.Header.Set("Authorization", "Bearer "+in.Token)
	}

	if in.ContentType != "" {
		req.Header.Set("Content-Type", in.ContentType)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return APIResponse{}, fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return APIResponse{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}

	return APIResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(answer)}, nil
}

// preferencesCoveringTest names the test that proves the routes of the
// preferences of a person instead of a CLI: no CLI reads or changes them.
const preferencesCoveringTest = "TestPreferences"

// preferencesAllowEntries returns the console routes of the preferences of a
// person. The steps of preferencesCoveringTest drive both routes through the
// server of each store, with the personal tokens of two people.
func preferencesAllowEntries() []AllowEntry {
	reason := "Console route: no CLI reads or changes the preferences of a person. " +
		"The harness step calls it with the personal token of a person and checks the answer."

	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/me/preferences",
			CoveringTest: preferencesCoveringTest, Reason: reason,
		},
		{
			Method: http.MethodPatch, Path: "/api/dustpan/v1/me/preferences",
			CoveringTest: preferencesCoveringTest, Reason: reason,
		},
	}
}
