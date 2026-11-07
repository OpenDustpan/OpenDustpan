package render

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// TestContainment is the containment layer: checkFiles gets hostile paths
// directly, as if every other check had failed.
func TestContainment(t *testing.T) {
	t.Parallel()

	allowed := []string{fileTofuProgram, fileTofuValues}
	bad := []string{
		"../main.tf",
		"/etc/passwd",
		"/main.tf",
		"a/b",
		"./main.tf",
		"main.tf/",
		"main.tf\x00",
		"..",
		".",
		"",
		"main.tf/../../x",
		`..\main.tf`,
		"other.tf",
		"MAIN.TF",
		"main.tf ",
		"terraform.tfvars.json.bak",
		"Pulumi.x.yaml",
	}

	for _, p := range bad {
		err := checkFiles([]selfservice.File{{Path: p, Content: []byte("x")}}, allowed)
		if !errors.Is(err, selfservice.ErrInvalid) {
			t.Errorf("path %q: error = %v, want ErrInvalid", p, err)
		}
	}

	twice := []selfservice.File{{Path: fileTofuProgram, Content: nil}, {Path: fileTofuProgram, Content: nil}}
	if err := checkFiles(twice, allowed); !errors.Is(err, selfservice.ErrInvalid) {
		t.Errorf("a path twice: error = %v, want ErrInvalid", err)
	}

	big := []selfservice.File{{Path: fileTofuProgram, Content: make([]byte, maxFileBytes+1)}}
	if err := checkFiles(big, allowed); !errors.Is(err, selfservice.ErrLimit) {
		t.Errorf("a file over the limit: error = %v, want ErrLimit", err)
	}

	both := []selfservice.File{{Path: fileTofuProgram, Content: nil}, {Path: fileTofuValues, Content: nil}}
	if err := checkFiles(both, allowed); err != nil {
		t.Errorf("the allowed files: %v", err)
	}
}

// TestAllowedText sends hostile text to the allowlist of the program text.
func TestAllowedText(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "a b", "a\"b", "a\\b", "a$b", "a{b", "a%b", "a\nb", "a\x00b", "é"} {
		if allowedText(s, tofuSourceExtra, maxSourceBytes) {
			t.Errorf("allowedText(%q) = true, want false", s)
		}
	}

	if allowedText("aaa", tofuSourceExtra, 2) {
		t.Error("allowedText over the limit = true, want false")
	}

	if !allowedText("registry.example.com/a/b-c", tofuSourceExtra, maxSourceBytes) {
		t.Error("allowedText of a registry source = false, want true")
	}
}
