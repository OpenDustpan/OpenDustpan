package workspace_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

var errNoVersion = errors.New("no such version")

// versions serves one finalized state version, counts the opens, and closes
// each content with closeErr.
type versions struct {
	state    string
	marks    workspace.SensitiveMarks
	readErr  error
	closeErr error
	opens    *atomic.Int64
}

func (v versions) ReadState(context.Context, workspace.StateVersionID) (workspace.StateVersion, error) {
	var sv workspace.StateVersion

	if v.readErr != nil {
		return sv, v.readErr
	}

	sv.SensitiveMarks = v.marks

	return sv, nil
}

func (v versions) Open(context.Context, workspace.StateVersion) (io.ReadCloser, error) {
	v.opens.Add(1)

	return closeWith{Reader: bytes.NewReader([]byte(v.state)), err: v.closeErr}, nil
}

// closeWith is a reader whose Close answers err.
type closeWith struct {
	io.Reader

	err error
}

func (c closeWith) Close() error { return c.err }

func newVersions(readErr, closeErr error) versions {
	var v versions

	v.state, v.marks, v.readErr, v.closeErr, v.opens = revealState(), revealMarks(), readErr, closeErr, new(
		atomic.Int64,
	)

	return v
}

// TestRevealVersion proves that a reveal of a version answers the value of a
// masked path, opens the content once for each pass, and refuses a path
// outside the masked set.
func TestRevealVersion(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil || len(masked) == 0 {
		t.Fatalf("masked paths = %v %v, want at least one", masked, err)
	}

	v := newVersions(nil, nil)

	value, err := workspace.RevealVersion(t.Context(), v, "sv-1", masked[0], nil)
	if err != nil || value != "s3cr3t" {
		t.Fatalf("reveal = %#v %v, want s3cr3t", value, err)
	}

	if v.opens.Load() < 2 {
		t.Fatalf("opens = %d, want one for each pass", v.opens.Load())
	}

	_, err = workspace.RevealVersion(t.Context(), v, "sv-1", "/resources/0/instances/0/attributes/nope", nil)
	if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Fatalf("reveal of a path outside the masked set: %v, want ErrResourceNotFound", err)
	}
}

// TestRevealVersionReadError proves that a version that cannot be read fails
// before any content opens.
func TestRevealVersionReadError(t *testing.T) {
	t.Parallel()

	v := newVersions(errNoVersion, nil)

	if _, err := workspace.RevealVersion(t.Context(), v, "sv-1", "/resources/0", nil); !errors.Is(err, errNoVersion) {
		t.Fatalf("reveal = %v, want the read error", err)
	}

	if v.opens.Load() != 0 {
		t.Fatalf("opens = %d, want none", v.opens.Load())
	}
}

// TestRevealVersionLogsCloseError proves that a failed close of the content
// is logged with no value and does not fail the reveal.
func TestRevealVersionLogsCloseError(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil || len(masked) == 0 {
		t.Fatalf("masked paths = %v %v, want at least one", masked, err)
	}

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, nil))

	value, err := workspace.RevealVersion(t.Context(), newVersions(nil, errCloseState), "sv-1", masked[0], logger)
	if err != nil || value != "s3cr3t" {
		t.Fatalf("reveal = %#v %v, want s3cr3t", value, err)
	}

	if !strings.Contains(buf.String(), "close state content failed") || strings.Contains(buf.String(), "s3cr3t") {
		t.Fatalf("log %q, want the close failure and no value", buf.String())
	}
}
