package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// errNoHomeTest is the error of a platform with no home directory.
var errNoHomeTest = errors.New("no home")

func homeOfAlex() (string, error) { return "/home/alex", nil }

func noHomeDir() (string, error) { return "", errNoHomeTest }

// envOf answers a lookup of the environment that holds the values.
func getenvOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestPerUserDevDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		home    func() (string, error)
		want    string
		wantErr error
	}{
		{
			name: "linux with XDG_DATA_HOME", goos: "linux", env: map[string]string{"XDG_DATA_HOME": "/data/xdg"},
			home: homeOfAlex, want: "/data/xdg/opendustpan/dev", wantErr: nil,
		},
		{
			name: "linux without XDG_DATA_HOME", goos: "linux", env: nil, home: homeOfAlex,
			want: "/home/alex/.local/share/opendustpan/dev", wantErr: nil,
		},
		{
			name: "linux with a relative XDG_DATA_HOME", goos: "linux", env: map[string]string{"XDG_DATA_HOME": "rel"},
			home: homeOfAlex, want: "/home/alex/.local/share/opendustpan/dev", wantErr: nil,
		},
		{
			name: "freebsd follows the XDG rule", goos: "freebsd", env: nil, home: homeOfAlex,
			want: "/home/alex/.local/share/opendustpan/dev", wantErr: nil,
		},
		{
			name: "darwin", goos: "darwin", env: map[string]string{"XDG_DATA_HOME": "/ignored"}, home: homeOfAlex,
			want: "/home/alex/Library/Application Support/opendustpan/dev", wantErr: nil,
		},
		{
			name: "windows with LocalAppData", goos: "windows",
			env: map[string]string{"LocalAppData": "/c/Users/alex/AppData/Local"}, home: homeOfAlex,
			want: "/c/Users/alex/AppData/Local/opendustpan/dev", wantErr: nil,
		},
		{
			name: "windows without LocalAppData", goos: "windows", env: nil, home: homeOfAlex,
			want: "/home/alex/AppData/Local/opendustpan/dev", wantErr: nil,
		},
		{
			name:    "linux without a home directory",
			goos:    "linux",
			env:     nil,
			home:    noHomeDir,
			want:    "",
			wantErr: errDevHome,
		},
		{
			name: "linux without a home directory but with XDG_DATA_HOME", goos: "linux",
			env: map[string]string{"XDG_DATA_HOME": "/data/xdg"}, home: noHomeDir,
			want: "/data/xdg/opendustpan/dev", wantErr: nil,
		},
		{
			name:    "darwin without a home directory",
			goos:    "darwin",
			env:     nil,
			home:    noHomeDir,
			want:    "",
			wantErr: errDevHome,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := perUserDevDir(tt.goos, getenvOf(tt.env), tt.home)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if filepath.ToSlash(got) != tt.want {
				t.Fatalf("dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDevDirPathOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flagDir   string
		envParent string
		want      string
	}{
		{name: "the flag wins over the variable", flagDir: "/srv/trial", envParent: "/data", want: "/srv/trial"},
		{
			name: "the variable names a parent, and dev adds a subdirectory", flagDir: "", envParent: "/data",
			want: "/data/dev",
		},
		{
			name: "no setting gives the directory of the user", flagDir: "", envParent: "",
			want: "/home/alex/.local/share/opendustpan/dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := devDirPath(devDirInput{
				getenv: getenvOf(nil), homeDir: homeOfAlex, flagDir: tt.flagDir, envParent: tt.envParent, goos: "linux",
			})
			if err != nil {
				t.Fatalf("devDirPath: %v", err)
			}

			if got != tt.want {
				t.Fatalf("dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDevDirPathMakesRelativePathsAbsolute(t *testing.T) {
	t.Parallel()

	got, err := devDirPath(devDirInput{flagDir: "trial", goos: "linux", getenv: nil, homeDir: nil, envParent: ""})
	if err != nil {
		t.Fatalf("devDirPath: %v", err)
	}

	if !filepath.IsAbs(got) || filepath.Base(got) != "trial" {
		t.Fatalf("dir = %q, want an absolute path that ends in trial", got)
	}
}

// writeFile writes a file with the mode, whatever the umask.
func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

// chmodDir sets the mode of a directory, whatever the umask.
func chmodDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()

	if err := os.Chmod(dir, mode); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
}

// keyedDir makes a data directory with a key file and the mode of the
// directory.
func keyedDir(t *testing.T, mode os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, devKeyName), 0o600)
	chmodDir(t, dir, mode)

	return dir
}

func TestClaimDevDirClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T) string
		wantFresh bool
	}{
		{
			name:      "a directory that does not exist is made",
			setup:     func(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "a", "b", "dev") },
			wantFresh: true,
		},
		{
			name: "an empty directory is claimed",
			setup: func(t *testing.T) string {
				t.Helper()

				dir := t.TempDir()
				chmodDir(t, dir, 0o755)

				return dir
			},
			wantFresh: true,
		},
		{
			name: "a directory with only lost+found counts as empty",
			setup: func(t *testing.T) string {
				t.Helper()

				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, "lost+found"), 0o700); err != nil {
					t.Fatalf("mkdir: %v", err)
				}

				return dir
			},
			wantFresh: true,
		},
		{
			name:      "a directory with a key file is used again",
			setup:     func(t *testing.T) string { t.Helper(); return keyedDir(t, 0o700) },
			wantFresh: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := tt.setup(t)

			fresh, err := claimDevDir(dir)
			if err != nil {
				t.Fatalf("claimDevDir: %v", err)
			}

			if fresh != tt.wantFresh {
				t.Fatalf("fresh = %v, want %v", fresh, tt.wantFresh)
			}

			info, statErr := os.Stat(dir)
			if statErr != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("directory = %v, %v, want mode 0700", info, statErr)
			}
		})
	}
}

func TestClaimDevDirRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T) string
		// wantErr is text that the error holds.
		wantErr string
		// namesPath says whether the error names the path.
		namesPath bool
	}{
		{
			name: "a directory with files and no key file",
			setup: func(t *testing.T) string {
				t.Helper()

				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "dustpan.db"), 0o600)

				return dir
			},
			wantErr:   "holds files but no key file of opendustpan dev: dev refuses it. Set a new --data-dir.",
			namesPath: true,
		},
		{
			name:      "a directory that the group can enter",
			setup:     func(t *testing.T) string { t.Helper(); return keyedDir(t, 0o750) },
			wantErr:   "chmod 700",
			namesPath: true,
		},
		{
			name: "a path that is a file",
			setup: func(t *testing.T) string {
				t.Helper()

				path := filepath.Join(t.TempDir(), "file")
				writeFile(t, path, 0o600)

				return path
			},
			wantErr:   "not a directory",
			namesPath: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := tt.setup(t)

			_, err := claimDevDir(dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to hold %q", err, tt.wantErr)
			}

			if tt.namesPath && !strings.Contains(err.Error(), dir) {
				t.Fatalf("err = %q, want it to name %q", err, dir)
			}
		})
	}
}

// TestClaimDevDirOpensNoFile proves the refusal of a foreign directory comes
// before any file is made or opened: the directory holds what it held.
func TestClaimDevDirOpensNoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dustpan.db"), 0o600)

	if _, err := claimDevDir(dir); err == nil {
		t.Fatal("claimDevDir of a foreign directory succeeded")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "dustpan.db" {
		t.Fatalf("the directory holds %v after the refusal, want dustpan.db alone", entries)
	}
}

func TestCheckDevFileModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    os.FileMode
		class   pathClass
		wantErr bool
	}{
		{name: "secret file with mode 0600", mode: 0o600, class: privateSecret, wantErr: false},
		{name: "secret file the group can read", mode: 0o640, class: privateSecret, wantErr: true},
		{name: "secret file others can read", mode: 0o604, class: privateSecret, wantErr: true},
		{name: "plain file with mode 0644", mode: 0o644, class: plainFile, wantErr: false},
		{name: "plain file the group can write", mode: 0o660, class: plainFile, wantErr: true},
		{name: "plain file others can write", mode: 0o602, class: plainFile, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "file")
			writeFile(t, path, tt.mode)

			err := checkDevFile(path, tt.class)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, tt.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), path) {
				t.Fatalf("err = %q, want it to name the path", err)
			}
		})
	}

	if err := checkDevFile(filepath.Join(t.TempDir(), "missing"), privateSecret); err != nil {
		t.Fatalf("a file that does not exist: %v", err)
	}
}

// fakeFileInfo is an os.FileInfo whose Sys value the test chooses.
type fakeFileInfo struct {
	sys any
}

func (fakeFileInfo) Name() string       { return "dev" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return os.ModeDir | 0o700 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return true }
func (f fakeFileInfo) Sys() any         { return f.sys }

// probeStat answers the stat value of a file that the test makes. Its owner
// is the current user.
func probeStat(t *testing.T) *syscall.Stat_t {
	t.Helper()

	path := filepath.Join(t.TempDir(), "probe")
	writeFile(t, path, 0o600)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("the probe file has no stat value")
	}

	return stat
}

// statWithOwner answers a copy of the probe stat value with its owner set to
// uid. The copy keeps every other field of the platform.
func statWithOwner(t *testing.T, uid uint32) *syscall.Stat_t {
	t.Helper()

	stat := *probeStat(t)
	stat.Uid = uid

	return &stat
}

// checkOwnerCase runs checkDevOwner on a path with the given Sys value and
// checks the sentinel and the path in the error. A nil wantErr means no error.
func checkOwnerCase(t *testing.T, sys any, wantErr error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dev")

	err := checkDevOwner(path, fakeFileInfo{sys: sys})
	if wantErr == nil {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("err = nil, want it to match %v", wantErr)
	}

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want it to match %v", err, wantErr)
	}

	if !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %q, want it to name %q", err, path)
	}
}

func TestCheckDevOwner(t *testing.T) {
	t.Parallel()

	// The owner of a file that the test makes is the current user.
	current := probeStat(t).Uid

	tests := []struct {
		name string
		sys  any
		// wantErr is the sentinel the error matches, or nil for no error.
		wantErr error
	}{
		{name: "owner unknown when Sys is not a stat value", sys: nil, wantErr: errDevOwnerUnknown},
		{name: "owner is another user", sys: statWithOwner(t, current+1), wantErr: errDevOwner},
		{name: "owner is the current user", sys: statWithOwner(t, current), wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkOwnerCase(t, tt.sys, tt.wantErr)
		})
	}
}
