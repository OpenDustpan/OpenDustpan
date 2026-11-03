package apidesc_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// updatePin writes openapi.pin after a change of the description and of its
// version:
//
//	go test ./internal/api/apidesc -run TestVersionFollowsTheDescription -update-description-pin
//
//nolint:gochecknoglobals // A test flag is read once; passing it as a parameter adds nothing.
var updatePin = flag.Bool(
	"update-description-pin", false, "write openapi.pin from the checked-in description and its version",
)

// pinFile is the record of the last committed description: its info.version
// and the SHA-256 of its bytes. No server release exists yet, so the baseline
// is the committed description and not a released one. After the first
// release, a change of the description still compares with this file, and the
// file moves with each pull request that changes the description.
const pinFile = "openapi.pin"

// pinCheckEnv names the variable that runs TestVersionFollowsTheDescription
// while the check is disabled.
const pinCheckEnv = "DUSTPAN_TEST_DESCRIPTION_PIN"

// errPinStale means that the description and its version both changed, and the
// version rose: only the pin file is out of date.
var errPinStale = errors.New("the pin file is out of date")

// The errors of the pin check.
var (
	errPinForm      = errors.New("the pin file is not in the form 'version X.Y.Z' and 'sha256 <hex>'")
	errVersionForm  = errors.New("the version is not MAJOR.MINOR.PATCH")
	errBytesSame    = errors.New("the version changed and the description did not")
	errVersionSame  = errors.New("the description changed and the version did not")
	errVersionNotUp = errors.New("the version did not rise with the description")
)

// pin is the content of the pin file.
type pin struct {
	version string
	sum     string
}

// parsePin reads the two lines of the pin file. It refuses any other form.
func parsePin(raw []byte) (pin, error) {
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		return pin{}, fmt.Errorf("%w: %s has %d lines, want 2", errPinForm, pinFile, len(lines))
	}

	version, okVersion := strings.CutPrefix(lines[0], "version ")
	sum, okSum := strings.CutPrefix(lines[1], "sha256 ")

	if !okVersion || !okSum || len(sum) != sha256.Size*2 {
		return pin{}, fmt.Errorf("%w: %s", errPinForm, pinFile)
	}

	if _, err := parseVersion(version); err != nil {
		return pin{}, err
	}

	return pin{version: version, sum: sum}, nil
}

func (p pin) bytes() []byte {
	return []byte("version " + p.version + "\nsha256 " + p.sum + "\n")
}

// parseVersion reads MAJOR.MINOR.PATCH as three numbers.
func parseVersion(version string) ([3]int, error) {
	var out [3]int

	parts := strings.Split(version, ".")
	if len(parts) != len(out) {
		return out, fmt.Errorf("%w: %q", errVersionForm, version)
	}

	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || strconv.Itoa(n) != part {
			return out, fmt.Errorf("%w: %q", errVersionForm, version)
		}

		out[i] = n
	}

	return out, nil
}

// greater reports whether version a is higher than version b. Both parse.
func greater(a, b string) bool {
	va, errA := parseVersion(a)
	vb, errB := parseVersion(b)

	if errA != nil || errB != nil {
		return false
	}

	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}

	return false
}

// judge compares the description and its version with the pin. It returns nil
// when they agree. A change of the bytes with the same version fails, and so
// does a change of the version with the same bytes. A change of both passes
// only when the version rose, and then it returns errPinStale so that the
// update flag can write the new pin.
func judge(p pin, version string, doc []byte) error {
	digest := sha256.Sum256(doc)
	sum := hex.EncodeToString(digest[:])

	sameSum, sameVersion := sum == p.sum, version == p.version

	switch {
	case sameSum && sameVersion:
		return nil
	case sameSum:
		return fmt.Errorf("%w: info.version is %s and the pin has %s: restore DocumentVersion to %s",
			errBytesSame, version, p.version, p.version)
	case sameVersion:
		return fmt.Errorf("%w: info.version is still %s: "+
			"raise DocumentVersion in internal/api/apidesc/document.go, then run "+
			"go test ./internal/api/apidesc -run TestVersionFollowsTheDescription -update-description-pin",
			errVersionSame, version)
	case !greater(version, p.version):
		return fmt.Errorf("%w: info.version is %s and the pin has %s", errVersionNotUp, version, p.version)
	default:
		return fmt.Errorf("%w: run go test ./internal/api/apidesc -run TestVersionFollowsTheDescription "+
			"-update-description-pin", errPinStale)
	}
}

// TestVersionFollowsTheDescription fails when the bytes of the checked-in
// description change and info.version does not rise with them.
func TestVersionFollowsTheDescription(t *testing.T) {
	t.Parallel()

	// Disabled until the first release. Until then info.version stays at one
	// value while the description changes. At the first release, write the
	// pin again and remove this skip. The variable runs the check on demand.
	if os.Getenv(pinCheckEnv) == "" {
		t.Skip("disabled until the first release: info.version stays at one value until then; set " +
			pinCheckEnv + "=1 to run it")
	}

	raw, err := os.ReadFile(pinFile)
	if err != nil {
		t.Fatalf("read the pin: %v", err)
	}

	p, err := parsePin(raw)
	if err != nil {
		t.Fatal(err)
	}

	err = judge(p, apidesc.DocumentVersion, apidesc.Document())

	if errors.Is(err, errPinStale) && *updatePin {
		digest := sha256.Sum256(apidesc.Document())
		next := pin{version: apidesc.DocumentVersion, sum: hex.EncodeToString(digest[:])}

		if writeErr := os.WriteFile(pinFile, next.bytes(), 0o600); writeErr != nil {
			t.Fatalf("write the pin: %v", writeErr)
		}

		return
	}

	if err != nil {
		t.Error(err)
	}
}

// TestJudgeSeesEachChange sends each combination of a change of the bytes and
// a change of the version directly to the check.
func TestJudgeSeesEachChange(t *testing.T) {
	t.Parallel()

	doc := []byte(`{"a":1}`)
	digest := sha256.Sum256(doc)
	base := pin{version: "0.2.0", sum: hex.EncodeToString(digest[:])}

	tests := []struct {
		name    string
		version string
		doc     []byte
		want    error
	}{
		{"nothing changed", "0.2.0", doc, nil},
		{"bytes changed, version the same", "0.2.0", []byte(`{"a":2}`), errVersionSame},
		{"version changed, bytes the same", "0.3.0", doc, errBytesSame},
		{"bytes and version changed, pin stale", "0.3.0", []byte(`{"a":2}`), errPinStale},
		{"patch rose", "0.2.1", []byte(`{"a":2}`), errPinStale},
		{"version fell", "0.1.9", []byte(`{"a":2}`), errVersionNotUp},
		{"minor rose by more than one", "0.9.9", []byte(`{"a":2}`), errPinStale},
		{"whitespace in the bytes counts", "0.2.0", []byte("{\"a\":1}\n"), errVersionSame},
		{"empty bytes count", "0.2.0", nil, errVersionSame},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := judge(base, tt.version, tt.doc)

			if !errors.Is(err, tt.want) {
				t.Errorf("judge() = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestParsePinRefuses sends hostile pin files directly to the reader.
func TestParsePinRefuses(t *testing.T) {
	t.Parallel()

	sum := strings.Repeat("a", sha256.Size*2)

	for name, raw := range map[string]string{
		"empty":             "",
		"one line":          "version 0.2.0\n",
		"three lines":       "version 0.2.0\nsha256 " + sum + "\nextra\n",
		"swapped lines":     "sha256 " + sum + "\nversion 0.2.0\n",
		"short digest":      "version 0.2.0\nsha256 abc\n",
		"two-part version":  "version 0.2\nsha256 " + sum + "\n",
		"leading zero":      "version 0.02.0\nsha256 " + sum + "\n",
		"negative number":   "version 0.-2.0\nsha256 " + sum + "\n",
		"suffix on version": "version 0.2.0-rc1\nsha256 " + sum + "\n",
	} {
		if _, err := parsePin([]byte(raw)); err == nil {
			t.Errorf("%s: parsePin() accepted %q", name, raw)
		}
	}

	if _, err := parsePin([]byte("version 0.2.0\nsha256 " + sum + "\n")); err != nil {
		t.Errorf("parsePin() refused a valid pin: %v", err)
	}
}
