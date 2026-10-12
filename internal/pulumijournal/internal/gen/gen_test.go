package gen_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal/internal/gen"
)

// update rewrites the generated file instead of comparing with it.
// The drift test compares the file without it; make generate sets it.
//
//nolint:gochecknoglobals // A test flag is read once; passing it as a parameter adds nothing.
var update = flag.Bool("update", false, "rewrite the generated file instead of comparing with it")

// testDir answers the directory of this test file.
func testDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller holds no file")
	}
	return filepath.Dir(file)
}

// inputBytes reads the pinned upstream copy.
func inputBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testDir(t), "testdata", "journal.go"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// outputPath answers the path of the generated file.
func outputPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(filepath.Dir(testDir(t))), "entry_gen.go")
}

// TestGenerateMatchesCommittedFile checks that the committed file matches the generator output.
func TestGenerateMatchesCommittedFile(t *testing.T) {
	t.Parallel()
	got, err := gen.Generate(inputBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	out := outputPath(t)
	if *update {
		if werr := os.WriteFile(out, got, 0o600); werr != nil {
			t.Fatal(werr)
		}
		return
	}
	//nolint:gosec // The test reads the generated file at a fixed path under the package.
	want, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("entry_gen.go is out of date; run make generate")
	}
}

// TestGenerateRejectsChangedInput checks that a changed input fails with ErrInputHash.
func TestGenerateRejectsChangedInput(t *testing.T) {
	t.Parallel()
	changed := bytes.Replace(inputBytes(t), []byte("Copyright"), []byte("Xopyright"), 1)
	if _, err := gen.Generate(changed); !errors.Is(err, gen.ErrInputHash) {
		t.Fatalf("Generate with changed input did not return ErrInputHash: %v", err)
	}
}

// TestGenerateIsDeterministic checks that two calls give identical bytes.
func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()
	input := inputBytes(t)
	first, err := gen.Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, gerr := gen.Generate(input)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Generate gave different bytes on two calls with the same input")
	}
}
