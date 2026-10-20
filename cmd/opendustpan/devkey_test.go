package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevKeyRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), devKeyName)

	made, err := makeDevKey(path)
	if err != nil {
		t.Fatalf("makeDevKey: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads the key file that it made.
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if first, _, _ := strings.Cut(string(raw), "\n"); first != devKeyHeader {
		t.Fatalf("first line = %q, want the header of dev", first)
	}

	read, err := readDevKey(path)
	if err != nil {
		t.Fatalf("readDevKey: %v", err)
	}

	if !bytes.Equal(read, made) || len(read) != 32 {
		t.Fatalf("read key differs from the made key, or is not 32 bytes (%d)", len(read))
	}

	// A second make never overwrites the file.
	if _, again := makeDevKey(path); again == nil {
		t.Fatal("makeDevKey overwrote an existing key file")
	}
}

func TestReadDevKeyRefuses(t *testing.T) {
	t.Parallel()

	good := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		want    string
	}{
		{name: "key file of serve with no header", content: good + "\n", mode: 0o600, want: "first line"},
		{name: "another header", content: "something else\n" + good + "\n", mode: 0o600, want: "first line"},
		{name: "header and a key that is not base64", content: devKeyHeader + "\n%%%\n", mode: 0o600, want: "base64"},
		{name: "header and a key of the wrong size", content: devKeyHeader + "\nAAAA\n", mode: 0o600, want: "32"},
		{name: "file the group can read", content: devKeyHeader + "\n" + good + "\n", mode: 0o640, want: "chmod 600"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), devKeyName)
			if err := os.WriteFile(path, []byte(tt.content), tt.mode); err != nil {
				t.Fatalf("write: %v", err)
			}

			writeFileMode(t, path, tt.mode)

			_, err := readDevKey(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to hold %q", err, tt.want)
			}
		})
	}
}

// writeFileMode sets the mode of a file, whatever the umask.
func writeFileMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

const (
	testTokenEnv  = "0123456789abcdef0123456789abcdef-env"
	testTokenFile = "0123456789abcdef0123456789abcdef-file"
)

func TestLoadDevToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// fileContent is the content of the --admin-token-file file, empty for
		// no flag.
		fileContent string
		envValue    string
		hasEnv      bool
		wantSource  devTokenSource
		// wantValue is the token, or empty for a token that dev made.
		wantValue string
	}{
		{
			name: "no setting makes a token", fileContent: "", envValue: "", hasEnv: false, wantSource: tokenMade,
			wantValue: "",
		},
		{
			name: "the variable gives the token", fileContent: "", envValue: testTokenEnv, hasEnv: true,
			wantSource: tokenEnv, wantValue: testTokenEnv,
		},
		{
			name: "a blank variable counts as not set", fileContent: "", envValue: "  ", hasEnv: true,
			wantSource: tokenMade, wantValue: "",
		},
		{
			name: "the file gives the token", fileContent: testTokenFile + "\n", envValue: "", hasEnv: false,
			wantSource: tokenFile, wantValue: testTokenFile,
		},
		{
			name: "the flag wins over the variable", fileContent: testTokenFile + "\n", envValue: testTokenEnv,
			hasEnv: true, wantSource: tokenFile, wantValue: testTokenFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			flagFile := tokenFileWith(t, tt.fileContent)

			got, err := loadDevToken(
				devTokenInput{dir: dir, flagFile: flagFile, envValue: tt.envValue, hasEnvVal: tt.hasEnv},
			)
			if err != nil {
				t.Fatalf("loadDevToken: %v", err)
			}

			if got.source != tt.wantSource || (tt.wantValue != "" && got.value != tt.wantValue) {
				t.Fatalf("token = %+v, want the source %v and the value %q", got, tt.wantSource, tt.wantValue)
			}

			checkTokenFile(t, dir, got)
		})
	}
}

// tokenFileWith writes a token file with the content and answers its path,
// or answers nothing for no content.
func tokenFileWith(t *testing.T, content string) string {
	t.Helper()

	if content == "" {
		return ""
	}

	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	return path
}

// checkTokenFile proves the token file of the data directory: a token that
// dev made is at least 32 bytes, kept with mode 0600 and the same at the next
// start, and a token from a setting leaves no token file.
func checkTokenFile(t *testing.T, dir string, got devToken) {
	t.Helper()

	path := filepath.Join(dir, devTokenName)

	if got.source != tokenMade {
		if _, err := os.Stat(path); err == nil {
			t.Fatal("dev made a token file although a setting gave the token")
		}

		return
	}

	if len(got.value) < 32 {
		t.Fatalf("made token holds %d bytes, want at least 32", len(got.value))
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file = %v, %v, want mode 0600", info, err)
	}

	again, err := loadDevToken(devTokenInput{dir: dir, flagFile: "", envValue: "", hasEnvVal: false})
	if err != nil {
		t.Fatalf("loadDevToken again: %v", err)
	}

	if again.value != got.value || again.file != path {
		t.Fatalf("second load = %+v, want the same token from %s", again, path)
	}
}

func TestLoadDevTokenRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		text    string
	}{
		{
			name:    "two tokens",
			content: testTokenFile + "-a\n" + testTokenFile + "-b\n", mode: 0o600, text: errDevTokenFile.Error(),
		},
		{name: "file the group can read", content: testTokenFile + "\n", mode: 0o640, text: "chmod 600"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte(tt.content), tt.mode); err != nil {
				t.Fatalf("write: %v", err)
			}

			writeFileMode(t, path, tt.mode)

			_, err := loadDevToken(devTokenInput{dir: t.TempDir(), flagFile: path, envValue: "", hasEnvVal: false})
			if err == nil || !strings.Contains(err.Error(), tt.text) {
				t.Fatalf("err = %v, want it to hold %q", err, tt.text)
			}
		})
	}
}

func TestLoadDevTokenFileWithTwoTokensWrapsTheSentinel(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(testTokenFile+"-a\n"+testTokenFile+"-b\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := loadDevToken(devTokenInput{dir: t.TempDir(), flagFile: path, envValue: "", hasEnvVal: false})
	if !errors.Is(err, errDevTokenFile) {
		t.Fatalf("err = %v, want it to wrap errDevTokenFile", err)
	}
}
