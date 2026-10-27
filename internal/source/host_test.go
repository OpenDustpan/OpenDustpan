package source_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/source"
)

// hostileHosts returns hosts that the source code must never fetch from or send
// a credential to: IP literals, numeric forms that a resolver reads as an
// IPv4 address, localhost, and addresses in loopback, private, link-local,
// unique local, metadata and reserved ranges.
func hostileHosts() []struct{ name, host string } {
	return []struct{ name, host string }{
		{"loopback", "127.0.0.1"},
		{"loopback other", "127.1.2.3"},
		{"unspecified", "0.0.0.0"},
		{"private 10", "10.0.0.1"},
		{"private 172", "172.16.0.1"},
		{"private 192", "192.168.1.1"},
		{"link-local", "169.254.1.1"},
		{"cloud metadata", "169.254.169.254"},
		{"shared address space", "100.64.0.1"},
		{"multicast", "224.0.0.1"},
		{"broadcast", "255.255.255.255"},
		{"public IPv4", "93.184.216.34"},
		{"public IPv4 resolver", "8.8.8.8"},
		{"public IPv6", "[2606:4700:4700::1111]"},
		{"public IPv6 without brackets", "2606:4700:4700::1111"},
		{"upper-case hexadecimal", "0X7F000001"},
		{"upper-case hexadecimal digits", "0x7F000001"},
		{"decimal", "2130706433"},
		{"hexadecimal", "0x7f000001"},
		{"hexadecimal parts", "0x7f.0x0.0x0.0x1"},
		{"octal", "0177.0.0.1"},
		{"short form", "127.1"},
		{"mixed form", "0x7f.1"},
		{"localhost", "localhost"},
		{"localhost upper case", "LOCALHOST"},
		{"localhost with a dot", "localhost."},
		{"name under localhost", "git.localhost"},
		{"name under localhost upper case", "Git.LocalHost"},
		{"IPv6 loopback", "[::1]"},
		{"IPv6 unique local", "[fd00::1]"},
		{"IPv6 link-local", "[fe80::1]"},
		{"IPv6 mapped loopback", "[::ffff:127.0.0.1]"},
		{"IPv6 mapped metadata", "[::ffff:a9fe:a9fe]"},
		{"IPv6 documentation", "[2001:db8::1]"},
		{"IPv6 without brackets", "::1"},
		{"IPv6 mapped without brackets", "::ffff:127.0.0.1"},
	}
}

func TestHostileHostsResolveURL(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileHosts() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url := "https://" + tc.host + "/org/repo"

			for name, args := range map[string][2]string{
				"absolute ref":   {"https://git.example.com/org/repo", url},
				"superproject":   {url, "../other"},
				"same directory": {url, "./other"},
				"both":           {url, url},
			} {
				got, err := source.ResolveURL(args[0], args[1])
				if !errors.Is(err, source.ErrNotAllowed) || got != "" {
					t.Errorf("%s: ResolveURL(%q, %q) = %q, %v, want ErrNotAllowed", name, args[0], args[1], got, err)
				}
			}
		})
	}
}

func TestHostileHostsSameHost(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileHosts() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url := "https://" + tc.host + "/org/repo"

			// The credentials of a connection go to a URL only when both
			// hosts are one host. A hostile host is never that host.
			if source.SameHost(url, url) {
				t.Errorf("SameHost(%q, %q) = true, want false", url, url)
			}

			if source.SameHost(url, "https://git.example.com/org/repo") ||
				source.SameHost("https://git.example.com/org/repo", url) {
				t.Errorf("SameHost with %q and a public host = true, want false", url)
			}
		})
	}
}

func TestHostileHostsLFSEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileHosts() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url := "https://" + tc.host + "/org/repo"

			got, err := source.LFSEndpoint(url)
			if !errors.Is(err, source.ErrNotAllowed) || got != "" {
				t.Errorf("LFSEndpoint(%q) = %q, %v, want ErrNotAllowed", url, got, err)
			}
		})
	}
}

// TestPublicHostsStillPass keeps the hostile list honest: a name that looks
// like an address but is a name, and an ordinary name, still pass.
func TestPublicHostsStillPass(t *testing.T) {
	t.Parallel()

	for _, host := range []string{
		"git.example.com",
		"localhost.example.com",
		"127.0.0.1.example.com",
		"0x7f.example.com",
		"1e3.example.com",
		"xn--gt-9ja.example.com",
	} {
		url := "https://" + host + "/org/repo"

		if _, err := source.LFSEndpoint(url); err != nil {
			t.Errorf("LFSEndpoint(%q) = %v, want no error", url, err)
		}

		if !source.SameHost(url, url) {
			t.Errorf("SameHost(%q, %q) = false, want true", url, url)
		}
	}
}
