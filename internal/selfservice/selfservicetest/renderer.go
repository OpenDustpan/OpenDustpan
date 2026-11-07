package selfservicetest

import (
	"context"
	"errors"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// RenderCase is one render that must succeed. Files holds the golden
// content of each file, by path; the render must give these files and no
// other file.
type RenderCase struct {
	Name      string
	Input     selfservice.RenderInput
	Operation selfservice.Operation
	// Keys are the config keys that the render reports, in the form of
	// selfservice.NormalizeKey, sorted.
	Keys  []string
	Files map[string]string
}

// RenderFailure is one render that must fail with Err and give no file and
// no key.
type RenderFailure struct {
	Name  string
	Input selfservice.RenderInput
	Err   error
}

// RendererSuite is what RunRenderer runs: the renderer, the tool name that
// it must report, and the cases.
type RendererSuite struct {
	Renderer selfservice.Renderer
	Tool     string
	Cases    []RenderCase
	Failures []RenderFailure
}

// RunRenderer is the conformance suite of selfservice.Renderer. Every
// renderer runs it. A renderer must:
//   - report its tool name,
//   - give the golden files, the operation and the keys of each case,
//   - give the same bytes for two renders of one input,
//   - give only relative, clean paths with no duplicate,
//   - give the keys sorted and with no duplicate,
//   - fail each failure case with its error and give no file,
//   - fail a canceled context.
func RunRenderer(t *testing.T, s RendererSuite) {
	t.Helper()

	t.Run("tool", func(t *testing.T) {
		t.Parallel()

		if got := s.Renderer.Tool(); got != s.Tool {
			t.Fatalf("Tool() = %q, want %q", got, s.Tool)
		}
	})

	for i := range s.Cases {
		c := &s.Cases[i]

		t.Run("render/"+c.Name, func(t *testing.T) {
			t.Parallel()
			checkRender(t, s.Renderer, c)
		})
	}

	for i := range s.Failures {
		f := &s.Failures[i]

		t.Run("fails/"+f.Name, func(t *testing.T) {
			t.Parallel()
			checkFailure(t, s.Renderer, f)
		})
	}

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		if len(s.Cases) == 0 {
			t.Skip("no case")
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		got, err := s.Renderer.Render(ctx, s.Cases[0].Input)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Render error = %v, want context.Canceled", err)
		}

		if len(got.Files) != 0 {
			t.Fatalf("a failed render gave %d files, want none", len(got.Files))
		}
	})
}

func checkFailure(t *testing.T, r selfservice.Renderer, f *RenderFailure) {
	t.Helper()

	got, err := r.Render(t.Context(), f.Input)
	if !errors.Is(err, f.Err) {
		t.Fatalf("Render error = %v, want %v", err, f.Err)
	}

	if len(got.Files) != 0 || len(got.Keys) != 0 {
		t.Fatalf("a failed render gave %d files and %d keys, want none", len(got.Files), len(got.Keys))
	}
}

func checkRender(t *testing.T, r selfservice.Renderer, c *RenderCase) {
	t.Helper()

	first, err := r.Render(t.Context(), c.Input)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	second, err := r.Render(t.Context(), c.Input)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two renders of one input differ:\n%+v\n%+v", first, second)
	}

	if first.Operation != c.Operation {
		t.Errorf("Operation = %q, want %q", first.Operation, c.Operation)
	}

	checkKeys(t, first.Keys, c.Keys)
	checkFiles(t, first.Files, c.Files)
}

func checkKeys(t *testing.T, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("Keys = %q, want %q", got, want)
	}

	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("Keys %q are not sorted and unique", got)
	}
}

func checkFiles(t *testing.T, got []selfservice.File, want map[string]string) {
	t.Helper()

	seen := make(map[string]bool, len(got))

	for i := range got {
		f := &got[i]

		if !relativeName(f.Path) {
			t.Errorf("file path %q is not a relative file name", f.Path)
		}

		if seen[f.Path] {
			t.Errorf("file path %q appears twice", f.Path)
		}

		seen[f.Path] = true

		content, ok := want[f.Path]

		switch {
		case !ok:
			t.Errorf("unexpected file %q", f.Path)
		case string(f.Content) != content:
			t.Errorf("file %q:\n%s\nwant:\n%s", f.Path, f.Content, content)
		default:
		}
	}

	for p := range want {
		if !seen[p] {
			t.Errorf("missing file %q", p)
		}
	}
}

// relativeName reports whether p is a clean name of a file in the render
// directory.
func relativeName(p string) bool {
	return p != "" && !path.IsAbs(p) && path.Clean(p) == p && !strings.Contains(p, "..") && !strings.Contains(p, "/")
}
