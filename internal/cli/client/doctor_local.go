package client

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
)

const (
	// adminTokenMinBytes is the shortest bootstrap admin token that doctor
	// accepts.
	adminTokenMinBytes = 32
	// keyFileMinBytes and keyFileMaxBytes bound the size of a key file: 32
	// bytes in base64 are 44 characters, with a line end of one or two.
	keyFileMinBytes = 44
	keyFileMaxBytes = 46
	// keyFileLooseBits are the permission bits that a key file must not set:
	// everything except the read and the write of the owner.
	keyFileLooseBits = 0o177
)

// localChecks runs the checks that need the settings only, in the order of
// the table of the checks.
func localChecks(d doctorSettings) []checkResult {
	results := keyChecks(d)

	results = append(results, databaseCheck(d), adminTokenCheck(d), backupTargetCheck(d))

	return append(results, listenCheck(d), proxyCheck(d))
}

// keyChecks checks that the operator set a key-encryption key and, for a key
// file, that the file has the right mode and size and sits away from the
// data. It asks the file system for the mode and the size. It does not read
// the file.
func keyChecks(d doctorSettings) []checkResult {
	if value, set := d.lookup("DUSTPAN_KEY_ENCRYPTION_KEY"); set && strings.TrimSpace(value) != "" {
		return []checkResult{
			{statusPass, nameKey, "the key-encryption key is set in DUSTPAN_KEY_ENCRYPTION_KEY"},
			{statusPass, nameKeyPlace, "the key is not in a file"},
		}
	}

	path := d.value("key-encryption-key-file")
	if path == "" {
		return []checkResult{{
			statusFail, nameKey,
			"no key-encryption key is set: set --key-encryption-key-file (DUSTPAN_KEY_ENCRYPTION_KEY_FILE) " +
				"or DUSTPAN_KEY_ENCRYPTION_KEY. To make a key file, run: opendustpan key generate --out <path>",
		}}
	}

	key := checkKeyFile(path)
	if key.status == statusFail {
		return []checkResult{key}
	}

	place := checkResult{statusPass, nameKeyPlace, "the key file is not next to the data"}

	if cli.KeyNextToData(path, d.value("database"), d.value("blob-dir")) {
		place = checkResult{
			statusWarn,
			nameKeyPlace,
			"the key file is next to the data: a backup of the data holds the key too. Move the key file to another directory",
		}
	}

	return []checkResult{key, place}
}

// checkKeyFile checks the mode and the size of the key file at path. The check
// uses stat and never reads the key.
func checkKeyFile(path string) checkResult {
	info, err := os.Stat(path)
	if err != nil {
		return checkResult{
			statusFail,
			nameKey,
			fmt.Sprintf("the key file %s cannot be read: %v", path, errors.Unwrap(err)),
		}
	}

	switch {
	case !info.Mode().IsRegular():
		return checkResult{statusFail, nameKey, fmt.Sprintf("the key file %s is not a regular file", path)}
	case info.Mode().Perm()&keyFileLooseBits != 0:
		return checkResult{
			statusFail, nameKey,
			fmt.Sprintf("the key file %s has mode %04o: run chmod 600 on the file", path, info.Mode().Perm()),
		}
	case info.Size() < keyFileMinBytes || info.Size() > keyFileMaxBytes:
		return checkResult{
			statusFail, nameKey,
			fmt.Sprintf("the key file %s has %d bytes, but a base64 key of 32 bytes has 44 characters: "+
				"make a new file with: opendustpan key generate --out <path>", path, info.Size()),
		}
	default:
		return checkResult{
			statusPass,
			nameKey,
			fmt.Sprintf("the key file %s has mode %04o and the size of a key", path, info.Mode().Perm()),
		}
	}
}

// databaseCheck checks that the operator set a database. The detail never
// shows the value: it can hold a password.
func databaseCheck(d doctorSettings) checkResult {
	if d.value("database") == "" {
		return checkResult{
			statusFail, nameDatabase,
			"no database is set: set --database (DUSTPAN_DATABASE), for example sqlite:/var/lib/opendustpan/dustpan.db",
		}
	}

	return checkResult{statusPass, nameDatabase, "the database is set"}
}

// adminTokenCheck checks that each bootstrap admin token has at least 32
// bytes. No token is a pass: the operator may sign in another way. The
// detail never shows a token.
func adminTokenCheck(d doctorSettings) checkResult {
	tokens, err := LoadAdminTokensFrom(d.lookup)

	switch {
	case errors.Is(err, ErrAdminTokenMissing):
		return checkResult{statusPass, nameAdminToken, "no bootstrap admin token is set"}
	case err != nil:
		return checkResult{statusFail, nameAdminToken, err.Error()}
	}

	for _, token := range tokens {
		if len(strings.TrimSpace(token)) < adminTokenMinBytes {
			return checkResult{
				statusFail,
				nameAdminToken,
				fmt.Sprintf(
					"an admin token has fewer than %d bytes: make a longer one, for example with: openssl rand -hex 32",
					adminTokenMinBytes,
				),
			}
		}
	}

	return checkResult{
		statusPass,
		nameAdminToken,
		fmt.Sprintf("each admin token has at least %d bytes", adminTokenMinBytes),
	}
}

// backupTargetCheck warns when the operator set no backup target.
func backupTargetCheck(d doctorSettings) checkResult {
	if d.value("backup-target") == "" {
		return checkResult{
			statusWarn,
			nameBackupTarget,
			"no backup target is set: set --backup-target (DUSTPAN_BACKUP_TARGET), for example dir:/var/backups/dustpan",
		}
	}

	return checkResult{statusPass, nameBackupTarget, "a backup target is set"}
}

// listenCheck warns about plain HTTP on an address that is not loopback, when
// the operator set no TLS and no public URL with https that names a proxy.
func listenCheck(d doctorSettings) checkResult {
	listen := d.value("listen")
	tls := d.value("tls-cert") != "" && d.value("tls-key") != ""

	switch {
	case isLoopbackListen(listen):
		return checkResult{statusPass, nameListen, listen + " is a loopback address"}
	case tls:
		return checkResult{statusPass, nameListen, listen + " uses TLS"}
	case httpsPublicURL(d.value("public-url")):
		return checkResult{statusPass, nameListen, listen + " is behind a proxy that --public-url names with https"}
	default:
		return checkResult{
			statusWarn, nameListen,
			listen + " is not a loopback address and uses plain HTTP: set --tls-cert and --tls-key, " +
				"or set --public-url to the https address of the proxy in front",
		}
	}
}

// proxyCheck warns when the operator set --public-url and no trusted proxies.
func proxyCheck(d doctorSettings) checkResult {
	switch {
	case d.value("public-url") == "":
		return checkResult{statusPass, nameProxies, "no public URL is set"}
	case d.value("trusted-proxies") == "":
		return checkResult{
			statusWarn, nameProxies,
			"--public-url is set and --trusted-proxies is not: the server reads the address of each client " +
				"from the proxy, so set --trusted-proxies (DUSTPAN_TRUSTED_PROXIES) to the addresses of the proxy",
		}
	default:
		return checkResult{statusPass, nameProxies, "--trusted-proxies is set"}
	}
}

// isLoopbackListen reports whether the listen address names loopback only. An
// address with no host, like :8080, listens on every interface.
func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host == "" {
		return false
	}

	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// httpsPublicURL reports whether rawURL is an https URL.
func httpsPublicURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil {
		return false
	}

	return parsed.Scheme == "https"
}
