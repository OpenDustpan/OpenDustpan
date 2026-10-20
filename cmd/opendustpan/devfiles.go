package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// errDevServerURL reports a server URL with no host.
var errDevServerURL = errors.New("the server URL is not valid")

// devUser is the user name that dev.env gives to OpenTofu. OpenTofu sends
// basic authentication only with a user name, and the server ignores it.
const devUser = "dustpan"

// devEnvInput holds what dev.env and dev.tfrc need.
type devEnvInput struct {
	// serverURL is the URL of the server, for example http://127.0.0.1:8080.
	serverURL string
	// envFile is the path of dev.env, and tfrcFile the path of dev.tfrc.
	envFile  string
	tfrcFile string
	// token is the administrator token and where it comes from.
	token devToken
	// bundleFile is the path of the certificate bundle for SSL_CERT_FILE, or
	// empty when dev.env sets none.
	bundleFile string
}

// shellQuote quotes s for a POSIX sh: one pair of single quotes, with each
// single quote inside ended, escaped and started again.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// renderDevEnv answers the text of dev.env: a POSIX sh file that sets the
// URL of the server and the credentials of the Pulumi CLI, of OpenTofu and
// of the client commands of opendustpan. A token that dev made goes into the
// file. A token from DUSTPAN_DEV_ADMIN_TOKEN stays in the variable, which the
// file reads in the shell that loads it. A token from --admin-token-file
// stays in that file, and the file names only the path.
func renderDevEnv(in devEnvInput) string {
	var out strings.Builder

	out.WriteString("# Settings of opendustpan dev. Load them in your shell:\n")
	out.WriteString("#   . " + shellQuote(in.envFile) + "\n")
	out.WriteString("export PULUMI_BACKEND_URL=" + shellQuote(in.serverURL) + "\n")
	out.WriteString("export DUSTPAN_SERVER=" + shellQuote(in.serverURL) + "\n")
	out.WriteString("export TF_CLI_CONFIG_FILE=" + shellQuote(in.tfrcFile) + "\n")
	out.WriteString("export TF_HTTP_USERNAME=" + shellQuote(devUser) + "\n")
	out.WriteString("export DUSTPAN_DEV=1\n")

	switch in.token.source {
	case tokenMade:
		for _, name := range []string{"DUSTPAN_ADMIN_TOKEN", "PULUMI_ACCESS_TOKEN", "TF_HTTP_PASSWORD"} {
			out.WriteString("export " + name + "=" + shellQuote(in.token.value) + "\n")
		}
	case tokenEnv:
		for _, name := range []string{"DUSTPAN_ADMIN_TOKEN", "PULUMI_ACCESS_TOKEN", "TF_HTTP_PASSWORD"} {
			out.WriteString("export " + name + "=\"${" + devAdminTokenEnv + ":?}\"\n")
		}
	case tokenFile:
		out.WriteString("export DUSTPAN_ADMIN_TOKEN_FILE=" + shellQuote(in.token.file) + "\n")
	default:
	}

	if in.bundleFile != "" {
		out.WriteString("export SSL_CERT_FILE=" + shellQuote(in.bundleFile) + "\n")
		out.WriteString("unset SSL_CERT_DIR\n")
	}

	return out.String()
}

// devAdminTokenEnv names the variable that holds the token of dev, for the
// text of dev.env.
const devAdminTokenEnv = "DUSTPAN_DEV_ADMIN_TOKEN" //nolint:gosec // The name of a variable, not a credential.

// renderDevTFRC answers the text of dev.tfrc: the CLI configuration of
// OpenTofu with one credentials block for the host of the server. No
// TF_TOKEN_ variable can name a host with a port, so the token is in the
// file, whatever its source.
func renderDevTFRC(serverURL, token string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("%w: %q", errDevServerURL, serverURL)
	}

	return "credentials " + strconv.Quote(parsed.Host) + " {\n  token = " + strconv.Quote(token) + "\n}\n", nil
}

// writePrivateFile writes data to path with mode 0600. It writes a new file
// in the same directory and renames it, so a reader never sees half of the
// data, and it replaces the file of an earlier start.
func writePrivateFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	cleanup := func(cause error) error {
		_ = tmp.Close()           //nolint:errcheck // The first error is the one that matters.
		_ = os.Remove(tmp.Name()) //nolint:errcheck // The first error is the one that matters.

		return cause
	}

	if chmodErr := tmp.Chmod(devFilePerm); chmodErr != nil {
		return cleanup(fmt.Errorf("protect %s: %w", path, chmodErr))
	}

	if _, writeErr := tmp.Write(data); writeErr != nil {
		return cleanup(fmt.Errorf("write %s: %w", path, writeErr))
	}

	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmp.Name()) //nolint:errcheck // The first error is the one that matters.

		return fmt.Errorf("write %s: %w", path, closeErr)
	}

	if renameErr := os.Rename(tmp.Name(), path); renameErr != nil {
		_ = os.Remove(tmp.Name()) //nolint:errcheck // The first error is the one that matters.

		return fmt.Errorf("write %s: %w", path, renameErr)
	}

	return nil
}

// devAddr is the content of dev.addr: an address that a client can dial, the
// path of the certificate, the process ID and the start time of dev.
type devAddr struct {
	Started time.Time `json:"started"`
	Addr    string    `json:"addr"`
	Cert    string    `json:"cert"`
	PID     int       `json:"pid"`
}

// loopbackV4 is the IPv4 loopback address that a client dials for a
// wildcard listen address.
//
//nolint:gochecknoglobals // A frozen value, as net.IPv4zero is.
var loopbackV4 = net.ParseIP("127.0.0.1")

// dialAddress answers the address that a client can dial for the address of
// a listener: a wildcard host becomes the loopback address of its family,
// and an IPv6 address stays in brackets.
func dialAddress(listen net.Addr) string {
	tcp, ok := listen.(*net.TCPAddr)
	if !ok {
		return listen.String()
	}

	host := tcp.IP
	if host == nil || host.IsUnspecified() {
		host = loopbackV4
		if tcp.IP != nil && tcp.IP.To4() == nil {
			host = net.IPv6loopback
		}
	}

	return net.JoinHostPort(host.String(), strconv.Itoa(tcp.Port))
}

// writeDevAddr writes dev.addr in dir with mode 0600.
func writeDevAddr(dir string, addr devAddr) error {
	data, err := json.Marshal(addr)
	if err != nil {
		return fmt.Errorf("encode %s: %w", devAddrName, err)
	}

	return writePrivateFile(filepath.Join(dir, devAddrName), append(data, '\n'))
}

// removeDevAddr deletes dev.addr in dir. A missing file is not an error.
func removeDevAddr(dir string) error {
	err := os.Remove(filepath.Join(dir, devAddrName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", devAddrName, err)
	}

	return nil
}
