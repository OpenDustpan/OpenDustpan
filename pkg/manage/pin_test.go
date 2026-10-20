package manage_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/manage"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	valid := map[string]string{
		"1.2.3":          "1.2.3",
		"v0.12.0":        "0.12.0",
		"1.0.0-rc.1":     "1.0.0-rc.1",
		"1.0.0+build.5":  "1.0.0",
		"0.0.0-alpha-1":  "0.0.0-alpha-1",
		"10.20.30-0.3.7": "10.20.30-0.3.7",
	}

	for text, want := range valid {
		got, err := manage.ParseVersion(text)
		if err != nil {
			t.Errorf("ParseVersion(%q) error: %v", text, err)

			continue
		}

		if got.String() != want {
			t.Errorf("ParseVersion(%q) = %s, want %s", text, got, want)
		}
	}

	invalid := []string{
		"", "dev", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.x", "1.2.-3", "-1.2.3",
		"1.2.3-", "1.2.3-rc..1", "1.2.3-01", "1.2.3-rc_1", "1.2.3 ", " 1.2.3", "1.2.3\n",
		"99999999999999999999.0.0", strings.Repeat("1", 65),
	}

	for _, text := range invalid {
		if _, err := manage.ParseVersion(text); !errors.Is(err, manage.ErrInvalidVersion) {
			t.Errorf("ParseVersion(%q) error = %v, want ErrInvalidVersion", text, err)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	t.Parallel()

	// Each row is in ascending order by the rules of semantic versioning.
	ascending := []string{
		"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0",
	}

	for i, low := range ascending {
		lowVersion, err := manage.ParseVersion(low)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", low, err)
		}

		for j, high := range ascending {
			highVersion, err := manage.ParseVersion(high)
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", high, err)
			}

			var want int

			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			default:
				want = 0
			}

			if got := lowVersion.Compare(highVersion); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", low, high, got, want)
			}
		}
	}
}

func TestParsePin(t *testing.T) {
	t.Parallel()

	pin, err := manage.ParsePin(">=0.12.0 <0.13.0")
	if err != nil {
		t.Fatalf("ParsePin: %v", err)
	}

	if pin.String() != ">=0.12.0 <0.13.0" {
		t.Errorf("String() = %q", pin.String())
	}

	invalid := []string{
		"", "0.12.0", ">=0.12.0", ">=0.12.0 <0.12.0", ">=0.13.0 <0.12.0", "<0.13.0 >=0.12.0",
		">=0.12 <0.13.0", ">=0.12.0 <0.13.0 <0.14.0", ">0.12.0 <0.13.0", ">=dev <0.13.0",
	}

	for _, text := range invalid {
		if _, err := manage.ParsePin(text); !errors.Is(err, manage.ErrInvalidVersion) {
			t.Errorf("ParsePin(%q) error = %v, want ErrInvalidVersion", text, err)
		}
	}
}

type pinCase struct {
	name   string
	server string
	pin    manage.Pin
	// wantErr is nil when the server is in the pin.
	wantErr error
	// wantText lists texts that the message must hold.
	wantText []string
}

func accepted(name, server string, pin manage.Pin) pinCase {
	return pinCase{name: name, server: server, pin: pin, wantErr: nil, wantText: nil}
}

func refused(name, server string, pin manage.Pin, wantErr error, wantText ...string) pinCase {
	return pinCase{name: name, server: server, pin: pin, wantErr: wantErr, wantText: wantText}
}

func TestCheckPin(t *testing.T) {
	t.Parallel()

	const pinText = ">=0.12.0 <0.13.0"

	release := mustPin(t, pinText)
	prerelease := mustPin(t, ">=0.12.0-rc.1 <0.13.0")
	zero := manage.Pin{
		Min: manage.Version{Major: 0, Minor: 0, Patch: 0, Pre: ""},
		Max: manage.Version{Major: 0, Minor: 0, Patch: 0, Pre: ""},
	}
	outside, invalid := manage.ErrVersionOutsidePin, manage.ErrInvalidVersion

	tests := []pinCase{
		accepted("lower bound", "0.12.0", release),
		accepted("inside", "0.12.7", release),
		accepted("leading v and build", "v0.12.3+abc", release),
		accepted("pre-release allowed by the pin", "0.12.0-rc.2", prerelease),
		accepted("release with a pre-release pin", "0.12.0", prerelease),
		refused("upper bound is outside", "0.13.0", release, outside, "0.13.0", pinText),
		refused("below", "0.11.9", release, outside, "0.11.9", pinText),
		refused("next major", "1.0.0", release, outside, "1.0.0", pinText),
		refused("pre-release of the next minor", "0.13.0-rc.1", release, outside, "0.13.0-rc.1", pinText),
		refused("pre-release of the lower bound", "0.12.0-rc.1", release, outside, "0.12.0-rc.1", pinText),
		refused("development build", "dev", release, invalid, `"dev"`, pinText),
		refused("empty version", "", release, invalid, `""`, pinText),
		refused("zero pin refuses all", "0.0.0", zero, outside, "0.0.0", ">=0.0.0 <0.0.0"),
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			checkPinCase(t, test)
		})
	}
}

func checkPinCase(t *testing.T, test pinCase) {
	t.Helper()

	err := manage.CheckPin(test.server, test.pin)
	if test.wantErr == nil {
		if err != nil {
			t.Fatalf("CheckPin(%q) error: %v", test.server, err)
		}

		return
	}

	if err == nil || !errors.Is(err, test.wantErr) {
		t.Errorf("CheckPin(%q) error = %v, want %v", test.server, err, test.wantErr)

		return
	}

	for _, text := range test.wantText {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("message %q lacks %q", err.Error(), text)
		}
	}
}

// TestCheckPinHostileServerText sends hostile text as the server version. The
// message must stay short and must not hold a raw control character.
func TestCheckPinHostileServerText(t *testing.T) {
	t.Parallel()

	pin := mustPin(t, ">=0.12.0 <0.13.0")
	hostile := "0.12.0\r\nX-Injected: yes" + strings.Repeat("A", 500)

	err := manage.CheckPin(hostile, pin)
	if err == nil || !errors.Is(err, manage.ErrInvalidVersion) {
		t.Errorf("CheckPin error = %v, want ErrInvalidVersion", err)

		return
	}

	if strings.ContainsAny(err.Error(), "\r\n") {
		t.Errorf("message holds a raw line break: %q", err.Error())
	}

	if len(err.Error()) > 400 {
		t.Errorf("message has %d characters, want at most 400", len(err.Error()))
	}
}

func mustPin(t *testing.T, text string) manage.Pin {
	t.Helper()

	pin, err := manage.ParsePin(text)
	if err != nil {
		t.Fatalf("ParsePin(%q): %v", text, err)
	}

	return pin
}
