package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/cli/client"
)

// devKeyHeader is the first line of the key file of dev. serve refuses a key
// file with this line, so a key of dev never wraps the secrets of a
// production database.
const devKeyHeader = "opendustpan dev key: this key is for a trial, and opendustpan serve refuses it"

// devTokenBytes is the size of a token that dev makes, before the encoding.
const devTokenBytes = 32

// Errors of the key file and of the token file of dev.
var (
	// errDevKeyFile reports a key file of dev that does not have the form
	// that dev writes.
	errDevKeyFile = errors.New("the key file of dev is not valid")
	// errDevTokenFile reports a token file with more or fewer than one token.
	errDevTokenFile = errors.New("the token file is not valid")
)

// makeDevKey writes a new key file of dev at path: the header line, then 32
// random bytes in base64. It never overwrites a file, and it syncs the file
// and its directory before it returns, as the key file of serve does. It
// returns the key.
func makeDevKey(path string) ([]byte, error) {
	key := make([]byte, cli.KeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("make the key-encryption key of dev: %w", err)
	}

	contents := devKeyHeader + "\n" + base64.StdEncoding.EncodeToString(key) + "\n"
	if err := cli.WriteKeyFile(path, contents, cli.WriteKeyContents); err != nil {
		return nil, err
	}

	return key, nil
}

// readDevKey reads the key file of dev at path. The file must belong to the
// current user and no other user can access it. It must start with the
// header line of dev.
func readDevKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read the key file of dev %s: %w", path, err)
	}

	if checkErr := checkDevPath(path, info, privateSecret); checkErr != nil {
		return nil, checkErr
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The path is the key file in the data directory of dev.
	if err != nil {
		return nil, fmt.Errorf("read the key file of dev %s: %w", path, err)
	}

	header, rest, found := strings.Cut(string(raw), "\n")
	if !found || header != devKeyHeader {
		return nil, fmt.Errorf("%s: %w: the first line is not the line of dev", path, errDevKeyFile)
	}

	return decodeKey(path, rest)
}

// devTokenSource says where the administrator token of dev comes from.
type devTokenSource int

const (
	// tokenMade is a token that dev made and keeps in the token file.
	tokenMade devTokenSource = iota
	// tokenEnv is a token from DUSTPAN_DEV_ADMIN_TOKEN.
	tokenEnv
	// tokenFile is a token from --admin-token-file.
	tokenFile
)

// devToken is the administrator token of dev and where it comes from. File
// is the path of the token file of dev, or the file of --admin-token-file.
type devToken struct {
	value  string
	file   string
	source devTokenSource
}

// devTokenInput holds what the choice of the token reads: the data
// directory, the flag --admin-token-file and the value of the variable
// DUSTPAN_DEV_ADMIN_TOKEN.
type devTokenInput struct {
	dir       string
	flagFile  string
	envValue  string
	hasEnvVal bool
}

// loadDevToken answers the administrator token of dev. --admin-token-file
// wins, then DUSTPAN_DEV_ADMIN_TOKEN. Else dev reads the token file in the
// data directory, and makes it with mode 0600 when it does not exist.
func loadDevToken(in devTokenInput) (devToken, error) {
	switch {
	case in.flagFile != "":
		return readDevTokenFile(in.flagFile)
	case in.hasEnvVal && strings.TrimSpace(in.envValue) != "":
		return devToken{value: strings.TrimSpace(in.envValue), file: "", source: tokenEnv}, nil
	}

	path := filepath.Join(in.dir, devTokenName)

	if _, err := os.Stat(path); err == nil {
		return readMadeToken(path)
	}

	raw := make([]byte, devTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return devToken{}, fmt.Errorf("make the administrator token of dev: %w", err)
	}

	value := base64.RawURLEncoding.EncodeToString(raw)
	if err := cli.WriteKeyFile(path, value+"\n", cli.WriteKeyContents); err != nil {
		return devToken{}, err
	}

	return devToken{value: value, file: path, source: tokenMade}, nil
}

// readMadeToken reads the token file that dev made earlier.
func readMadeToken(path string) (devToken, error) {
	info, err := os.Stat(path)
	if err != nil {
		return devToken{}, fmt.Errorf("read the token file of dev %s: %w", path, err)
	}

	if checkErr := checkDevPath(path, info, privateSecret); checkErr != nil {
		return devToken{}, checkErr
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The path is the token file in the data directory of dev.
	if err != nil {
		return devToken{}, fmt.Errorf("read the token file of dev %s: %w", path, err)
	}

	return devToken{value: strings.TrimSpace(string(raw)), file: path, source: tokenMade}, nil
}

// readDevTokenFile reads the file of --admin-token-file: the current user
// must own it, and no other user can access it. The file holds one token.
func readDevTokenFile(path string) (devToken, error) {
	info, err := os.Stat(path)
	if err != nil {
		return devToken{}, fmt.Errorf("read the token file %s: %w", path, err)
	}

	if checkErr := checkDevPath(path, info, privateSecret); checkErr != nil {
		return devToken{}, checkErr
	}

	tokens, err := client.ReadAdminTokenFile(path)
	if err != nil {
		return devToken{}, err
	}

	if len(tokens) != 1 {
		return devToken{}, fmt.Errorf(
			"%s: %w: dev takes one token, the file holds %d",
			path,
			errDevTokenFile,
			len(tokens),
		)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return devToken{}, fmt.Errorf("resolve %s: %w", path, err)
	}

	return devToken{value: tokens[0], file: abs, source: tokenFile}, nil
}
