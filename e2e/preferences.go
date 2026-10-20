//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ServerAPIStatusTyped sends one installation API request with token as a
// Bearer credential and contentType as the Content-Type, and returns its
// status, headers and body. A contentType of "" sends none. A step uses it for
// a route that needs a media type other than JSON, for example the merge
// patch of the preferences.
func (e *Env) ServerAPIStatusTyped(
	ctx context.Context, token, method, path, contentType, body string,
) (APIResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, strings.NewReader(body))
	if err != nil {
		return APIResponse{}, fmt.Errorf("build %s %s: %w", method, path, err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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
