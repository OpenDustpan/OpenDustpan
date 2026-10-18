package runner_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
)

func TestParseRelease(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want runner.Release
		ok   bool
	}{
		{"1.2.3", runner.Release{Major: 1, Minor: 2, Patch: 3, Pre: ""}, true},
		{"v0.14.0", runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""}, true},
		{"1.2", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.2.3.4", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.02.3", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.-2.3", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.2.3-rc.1", runner.Release{Major: 1, Minor: 2, Patch: 3, Pre: "rc.1"}, true},
		{"v1.2.3-rc1", runner.Release{Major: 1, Minor: 2, Patch: 3, Pre: "rc1"}, true},
		{"1.2.3+build.7", runner.Release{Major: 1, Minor: 2, Patch: 3, Pre: ""}, true},
		{"1.2.3-", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.2.3-rc..1", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.2.3+", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"1.+2.3", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
		{"", runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}, false},
	}

	for _, tc := range cases {
		got, err := runner.ParseRelease(tc.in)
		if tc.ok != (err == nil) || got != tc.want {
			t.Errorf("ParseRelease(%q) = %v, %v, want %v ok %v", tc.in, got, err, tc.want, tc.ok)
		}

		if tc.ok && got.String() != tc.in && "v"+got.String() != tc.in && !strings.Contains(tc.in, "+") {
			t.Errorf("String() = %q, want %q", got.String(), tc.in)
		}
	}
}

func TestCheckSkewAcceptsTwoOlderReleases(t *testing.T) {
	t.Parallel()

	r := func(major, minor, patch int) runner.Release {
		return runner.Release{Major: major, Minor: minor, Patch: patch, Pre: ""}
	}

	cases := []struct {
		name           string
		server, client runner.Release
		want           error
	}{
		{"same", r(0, 14, 0), r(0, 14, 0), nil},
		{"older patch", r(0, 14, 3), r(0, 14, 0), nil},
		{"newer patch", r(0, 14, 0), r(0, 14, 5), nil},
		{"one older", r(0, 14, 0), r(0, 13, 9), nil},
		{"two older", r(0, 14, 0), r(0, 12, 0), nil},
		{"three older", r(0, 14, 0), r(0, 11, 9), runner.ErrVersionUnsupported},
		{"newer minor", r(0, 14, 0), r(0, 15, 0), runner.ErrVersionAhead},
		{"newer major", r(0, 14, 0), r(1, 0, 0), runner.ErrVersionAhead},
		{"older major", r(1, 0, 0), r(0, 14, 0), runner.ErrVersionUnsupported},
		{"first releases", r(0, 1, 0), r(0, 0, 9), nil},
		{"pre-release of the server", runner.Release{Major: 0, Minor: 15, Patch: 0, Pre: "rc.1"}, r(0, 15, 0), nil},
		{"pre-release of the client", r(0, 14, 0), runner.Release{Major: 0, Minor: 14, Patch: 1, Pre: "rc.2"}, nil},
		{
			"pre-release ahead", r(0, 14, 0),
			runner.Release{Major: 0, Minor: 15, Patch: 0, Pre: "rc.1"},
			runner.ErrVersionAhead,
		},
	}

	for _, tc := range cases {
		err := runner.CheckSkew(tc.server, tc.client)
		if (tc.want == nil) != (err == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: CheckSkew(%s, %s) = %v, want %v", tc.name, tc.server, tc.client, err, tc.want)
		}

		if errors.Is(err, runner.ErrVersionAhead) && errors.Is(err, runner.ErrVersionUnsupported) {
			t.Errorf("%s: a newer client wraps ErrVersionUnsupported", tc.name)
		}
	}
}
