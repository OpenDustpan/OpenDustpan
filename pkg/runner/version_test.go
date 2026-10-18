package runner_test

import (
	"errors"
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
		{"1.2.3", runner.Release{Major: 1, Minor: 2, Patch: 3}, true},
		{"v0.14.0", runner.Release{Major: 0, Minor: 14, Patch: 0}, true},
		{"1.2", runner.Release{Major: 0, Minor: 0, Patch: 0}, false},
		{"1.2.3.4", runner.Release{Major: 0, Minor: 0, Patch: 0}, false},
		{"1.02.3", runner.Release{Major: 0, Minor: 0, Patch: 0}, false},
		{"1.-2.3", runner.Release{Major: 0, Minor: 0, Patch: 0}, false},
		{"1.2.3-rc1", runner.Release{Major: 0, Minor: 0, Patch: 0}, false},
		{"", runner.Release{Major: 0, Minor: 0, Patch: 0}, false},
	}

	for _, tc := range cases {
		got, err := runner.ParseRelease(tc.in)
		if tc.ok != (err == nil) || got != tc.want {
			t.Errorf("ParseRelease(%q) = %v, %v, want %v ok %v", tc.in, got, err, tc.want, tc.ok)
		}

		if tc.ok && got.String() != tc.in && "v"+got.String() != tc.in {
			t.Errorf("String() = %q, want %q", got.String(), tc.in)
		}
	}
}

func TestCheckSkewAcceptsTwoOlderReleases(t *testing.T) {
	t.Parallel()

	r := func(major, minor, patch int) runner.Release {
		return runner.Release{Major: major, Minor: minor, Patch: patch}
	}

	cases := []struct {
		name           string
		server, client runner.Release
		ok             bool
	}{
		{"same", r(0, 14, 0), r(0, 14, 0), true},
		{"older patch", r(0, 14, 3), r(0, 14, 0), true},
		{"newer patch", r(0, 14, 0), r(0, 14, 5), true},
		{"one older", r(0, 14, 0), r(0, 13, 9), true},
		{"two older", r(0, 14, 0), r(0, 12, 0), true},
		{"three older", r(0, 14, 0), r(0, 11, 9), false},
		{"newer minor", r(0, 14, 0), r(0, 15, 0), false},
		{"other major", r(1, 0, 0), r(0, 14, 0), false},
		{"first releases", r(0, 1, 0), r(0, 0, 9), true},
	}

	for _, tc := range cases {
		err := runner.CheckSkew(tc.server, tc.client)
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, runner.ErrVersionUnsupported)) {
			t.Errorf("%s: CheckSkew(%s, %s) = %v, want ok %v", tc.name, tc.server, tc.client, err, tc.ok)
		}
	}
}
