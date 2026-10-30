package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// toxiproxyTimeout bounds one call to the Toxiproxy API.
const toxiproxyTimeout = 10 * time.Second

// maxToxiproxyBody caps how much of a Toxiproxy answer the client reads.
const maxToxiproxyBody = 1 << 20

// Names of the two latency toxics of a proxy, one for each direction.
const (
	toxicUp   = "latency-up"
	toxicDown = "latency-down"
)

// errToxiproxyStatus reports a Toxiproxy answer with an unexpected status.
var errToxiproxyStatus = errors.New("toxiproxy answered an unexpected status")

// toxiproxy is a client of the Toxiproxy HTTP API.
type toxiproxy struct {
	base   string
	client *http.Client
}

// newToxiproxy answers a client of the API at base, for example
// http://127.0.0.1:18474.
func newToxiproxy(base string) (*toxiproxy, error) {
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: toxiproxy API URL %q", errBadFlag, base)
	}

	return &toxiproxy{base: parsed.String(), client: &http.Client{Timeout: toxiproxyTimeout}}, nil
}

// call sends one API request with a JSON body and checks the status.
func (t *toxiproxy) call(ctx context.Context, method, path string, body any, want ...int) error {
	var reader io.Reader = http.NoBody

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("toxiproxy %s %s: %w", method, path, err)
		}

		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, t.base+path, reader)
	if err != nil {
		return fmt.Errorf("toxiproxy %s %s: %w", method, path, err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("toxiproxy %s %s: %w", method, path, err)
	}

	answer, readErr := io.ReadAll(io.LimitReader(resp.Body, maxToxiproxyBody))
	if err := errors.Join(readErr, resp.Body.Close()); err != nil {
		return fmt.Errorf("toxiproxy %s %s: read answer: %w", method, path, err)
	}

	if slices.Contains(want, resp.StatusCode) {
		return nil
	}

	return fmt.Errorf(
		"toxiproxy %s %s: status %d: %.200s: %w",
		method,
		path,
		resp.StatusCode,
		answer,
		errToxiproxyStatus,
	)
}

// proxySpec is the body of a proxy create.
type proxySpec struct {
	Name     string `json:"name"`
	Listen   string `json:"listen"`
	Upstream string `json:"upstream"`
	Enabled  bool   `json:"enabled"`
}

// ensureProxy creates the proxy name from listen to upstream, after it
// deletes a proxy of that name from an earlier run.
func (t *toxiproxy) ensureProxy(ctx context.Context, name, listen, upstream string) error {
	err := t.call(
		ctx,
		http.MethodDelete,
		"/proxies/"+url.PathEscape(name),
		nil,
		http.StatusNoContent,
		http.StatusNotFound,
	)
	if err != nil {
		return err
	}

	spec := proxySpec{Name: name, Listen: listen, Upstream: upstream, Enabled: true}

	return t.call(ctx, http.MethodPost, "/proxies", spec, http.StatusCreated)
}

// latencyAttrs are the attributes of a latency toxic in milliseconds. The
// toxic delays each chunk by latency plus a uniform value in [-jitter,
// +jitter].
type latencyAttrs struct {
	Latency int `json:"latency"`
	Jitter  int `json:"jitter"`
}

// toxicSpec is the body of a toxic create.
type toxicSpec struct {
	Name       string       `json:"name"`
	Type       string       `json:"type"`
	Stream     string       `json:"stream"`
	Toxicity   float64      `json:"toxicity"`
	Attributes latencyAttrs `json:"attributes"`
}

// setLatency removes the latency toxics of proxy and adds the delay of link:
// half of the round trip on the request direction and the rest on the answer
// direction, each with the jitter of one direction.
func (t *toxiproxy) setLatency(ctx context.Context, proxy string, link link) error {
	base := "/proxies/" + url.PathEscape(proxy) + "/toxics"

	for _, name := range []string{toxicUp, toxicDown} {
		err := t.call(ctx, http.MethodDelete, base+"/"+name, nil, http.StatusNoContent, http.StatusNotFound)
		if err != nil {
			return err
		}
	}

	pair := link.split()

	for _, toxic := range []toxicSpec{
		{Name: toxicUp, Type: "latency", Stream: "upstream", Toxicity: 1, Attributes: pair.up},
		{Name: toxicDown, Type: "latency", Stream: "downstream", Toxicity: 1, Attributes: pair.down},
	} {
		if toxic.Attributes.Latency == 0 && toxic.Attributes.Jitter == 0 {
			continue
		}

		if err := t.call(ctx, http.MethodPost, base, toxic, http.StatusOK); err != nil {
			return err
		}
	}

	return nil
}
