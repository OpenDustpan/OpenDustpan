package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// devGuide names the page of the user documentation that shows how to trust
// the certificate of dev.
const devGuide = "docs/guides/dev-server.md"

// devBanner holds what the banner of dev shows. The banner never shows the
// administrator token.
type devBanner struct {
	tlsExpiry time.Time
	dir       string
	serverURL string
	// signIn is the text of the Sign in line.
	signIn  string
	envFile string
	// network is the text of the Network line, or empty for a loopback
	// address.
	network string
	// tokenNote is the text of the Token line, or empty.
	tokenNote string
	// placement is the text of the Placement line, or empty.
	placement string
	// trustNote is the text of the TLS line for a platform where dev cannot
	// write a certificate bundle.
	ephemeral bool
	trustStep bool
}

// writeDevBanner writes the banner of dev. It writes to stderr before the
// first log line, also with --log-format json.
func writeDevBanner(w io.Writer, b devBanner) error {
	var out strings.Builder

	out.WriteString("opendustpan dev: this server is for a trial or for CI. Do not use it in production.\n")

	data := b.dir
	if b.ephemeral {
		data += " (dev deletes this directory at stop, and with it the state that tracks the cloud resources of the trial)"
	}

	writeBannerLine(&out, "Data", data)
	writeBannerLine(&out, "Server", b.serverURL)

	if b.network != "" {
		writeBannerLine(&out, "Network", b.network)
	}

	if !b.tlsExpiry.IsZero() {
		text := "self-signed certificate, valid until " + b.tlsExpiry.UTC().Format(time.DateOnly)
		if b.trustStep {
			text += ". To trust it, see " + devGuide
		}

		writeBannerLine(&out, "TLS", text)
	}

	writeBannerLine(&out, "Sign in", b.signIn)

	if b.tokenNote != "" {
		writeBannerLine(&out, "Token", b.tokenNote)
	}

	if b.placement != "" {
		writeBannerLine(&out, "Rule", b.placement)
	}

	writeBannerLine(&out, "Settings", ". "+shellWord(b.envFile))

	out.WriteString("The key-encryption key is in the data directory. A backup of this directory holds the key.\n")
	out.WriteString("For production, run: opendustpan serve --help\n")

	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("write the banner: %w", err)
	}

	return nil
}

// writeBannerLine writes one line of the banner: the label with its colon,
// padded to the width of the longest label, and the text.
func writeBannerLine(out *strings.Builder, label, text string) {
	const width = len("Settings: ")

	fmt.Fprintf(out, "%-*s%s\n", width, label+":", text)
}

// shellWord answers path as one word for a POSIX sh: the path itself when it
// holds only safe characters, else quoted.
func shellWord(path string) string {
	for _, r := range path {
		safe := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("/._-+:@%", r)
		if !safe {
			return shellQuote(path)
		}
	}

	return path
}
