package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// KeyBytes is the size of a key-encryption key before the encoding.
const KeyBytes = 32

// Errors of key generate. Each one says what failed and what helps.
var (
	// ErrKeyOutMissing reports key generate with no path.
	ErrKeyOutMissing = errors.New(
		"no key file path is set: set --out <path>, for example: opendustpan key generate --out /etc/opendustpan/dustpan.key",
	)
	// ErrKeyOutInvalid reports a path with a control character.
	ErrKeyOutInvalid = errors.New("the key file path has a control character: set --out to a plain path")
	// ErrKeyGenerateArgs reports an argument that key generate does not take.
	ErrKeyGenerateArgs = errors.New("key generate takes no argument: set --out <path>")
)

// KeyGenerateCommand answers the key generate row of the command table. It is
// not a client of the REST API and it opens no store: it writes a new key
// file and reads no key, so a host with no other tool can make a key.
func KeyGenerateCommand() Command {
	return Command{
		Path:     []string{commandKey, commandGenerate},
		Synopsis: "Write a new key-encryption key file.",
		Settings: SettingsFor("out"),
		Run:      runKeyGenerate,
	}
}

// runKeyGenerate writes 32 random bytes from crypto/rand in base64 to the
// file that --out names, with owner-only mode. It refuses a file that
// exists. It prints the path and the warning that a lost key loses every
// secret. It never prints the key.
func runKeyGenerate(_ context.Context, args []string, stdout, stderr io.Writer) error {
	var out string

	fs := flag.NewFlagSet("key generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ByFlag("out").String(fs, &out)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return ErrKeyGenerateArgs
	}

	path, err := checkKeyOut(out)
	if err != nil {
		return err
	}

	if writeErr := writeNewKey(path); writeErr != nil {
		if errors.Is(writeErr, ErrKeyFileExists) {
			return fmt.Errorf("the key file exists: %s: set --out to a new path: %w", path, writeErr)
		}

		return fmt.Errorf("make the key file %s: %w", path, writeErr)
	}

	_, err = fmt.Fprintf(stdout, "key file: %s\n"+
		"A lost key loses every secret and every OpenTofu state that the key encrypts. "+
		"Nobody can recover them. Keep a copy of this file outside the data directory.\n"+
		"To use the key, set --key-encryption-key-file (DUSTPAN_KEY_ENCRYPTION_KEY_FILE) to this path.\n",
		path)
	if err != nil {
		return fmt.Errorf("write the report: %w", err)
	}

	return nil
}

// checkKeyOut checks the --out value at the boundary with an allowlist: a
// path of printable characters. It rejects a value that is not valid and does
// not change it, apart from the surrounding space.
func checkKeyOut(out string) (string, error) {
	path := strings.TrimSpace(out)
	if path == "" {
		return "", ErrKeyOutMissing
	}

	for _, r := range path {
		if r < ' ' || r == 0x7f {
			return "", ErrKeyOutInvalid
		}
	}

	return path, nil
}

// writeNewKey writes a new key to the file at path. The key stays in this
// function: it is cleared after the write and it goes to no log or report.
func writeNewKey(path string) error {
	key := make([]byte, KeyBytes)
	defer clear(key)

	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("make the key: %w", err)
	}

	return WriteKeyFile(path, base64.StdEncoding.EncodeToString(key)+"\n", WriteKeyContents)
}
