package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

// The names of the files in the data directory of dev.
const (
	devKeyName       = "dev.key"
	devTokenName     = "dev.token"
	devAddrName      = "dev.addr"
	devEnvName       = "dev.env"
	devTFRCName      = "dev.tfrc"
	devDBName        = "dev.db"
	devCertName      = "dev.crt"
	devTLSKeyName    = "dev.tls.key"
	devBundleName    = "dev.ca-bundle.crt"
	devDirSubdir     = "dev"
	devDataParent    = "opendustpan"
	devDirPerm       = 0o700
	devFilePerm      = 0o600
	devGroupOther    = 0o077
	devGroupOtherW   = 0o022
	lostAndFoundName = "lost+found"
)

// Errors of the data directory of dev. Each text names the path, and the one
// command that helps.
var (
	errDevHome = errors.New(
		"dev cannot find the home directory of the current user: set --data-dir or DUSTPAN_DATA_DIR",
	)
	errDevNotDir = errors.New("the data directory is not a directory: set a new --data-dir")
	// errDevForeign names the directory that holds files but no key file of
	// dev. dev opens no file in it.
	errDevForeign = errors.New("the directory holds files but no key file of opendustpan dev")
	errDevPrivate = errors.New("another user can access the path")
)

// devDirInput holds what the choice of the data directory reads. The fields
// that name a function let a test replace the platform.
type devDirInput struct {
	getenv  func(string) string
	homeDir func() (string, error)
	flagDir string
	// envParent is the value of DUSTPAN_DATA_DIR: a parent directory.
	envParent string
	goos      string
}

// devDirPath answers the data directory of dev for the order: --data-dir,
// then DUSTPAN_DATA_DIR with the subdirectory dev, then the directory for
// each user. The caller handles --ephemeral before it calls this function.
func devDirPath(in devDirInput) (string, error) {
	switch {
	case in.flagDir != "":
		abs, err := filepath.Abs(in.flagDir)
		if err != nil {
			return "", fmt.Errorf("resolve --data-dir: %w", err)
		}

		return abs, nil
	case in.envParent != "":
		parent, err := filepath.Abs(in.envParent)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", "DUSTPAN_DATA_DIR", err)
		}

		return filepath.Join(parent, devDirSubdir), nil
	default:
		return perUserDevDir(in.goos, in.getenv, in.homeDir)
	}
}

// perUserDevDir answers the directory of dev for each user: the data
// directory of the platform, then opendustpan/dev in it. On Linux and the
// other Unix systems the data directory is XDG_DATA_HOME when it holds an
// absolute path, else .local/share in the home directory.
func perUserDevDir(goos string, getenv func(string) string, homeDir func() (string, error)) (string, error) {
	var (
		base string
		err  error
	)

	switch goos {
	case "windows":
		base, err = windowsDataBase(getenv, homeDir)
	case "darwin":
		base, err = userHome(homeDir)
		base = filepath.Join(base, "Library", "Application Support")
	default:
		base, err = xdgDataBase(getenv, homeDir)
	}

	if err != nil {
		return "", err
	}

	return filepath.Join(base, devDataParent, devDirSubdir), nil
}

// userHome answers the home directory, or errDevHome.
func userHome(homeDir func() (string, error)) (string, error) {
	dir, err := homeDir()
	if err != nil || dir == "" {
		return "", errDevHome
	}

	return dir, nil
}

// windowsDataBase answers %LocalAppData%, or AppData\Local in the home
// directory when the variable is not set.
func windowsDataBase(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	if base := getenv("LocalAppData"); base != "" {
		return base, nil
	}

	dir, err := userHome(homeDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "AppData", "Local"), nil
}

// xdgDataBase answers XDG_DATA_HOME when it holds an absolute path, else
// .local/share in the home directory.
func xdgDataBase(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	if base := getenv("XDG_DATA_HOME"); filepath.IsAbs(base) {
		return base, nil
	}

	dir, err := userHome(homeDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, ".local", "share"), nil
}

// osDevDirInput answers the input of the choice of the data directory for
// the running process.
func osDevDirInput(flagDir, envParent string) devDirInput {
	return devDirInput{
		getenv: os.Getenv, homeDir: os.UserHomeDir, flagDir: flagDir, envParent: envParent, goos: runtime.GOOS,
	}
}

// claimDevDir makes sure dir is a data directory that dev may use, before dev
// opens or migrates any file in it. A directory that does not exist is made
// with mode 0700. An empty directory is claimed: dev sets mode 0700 on it.
// A directory that holds only lost+found counts as empty, because a mount
// root holds it. A directory with files and with no key file of dev is
// refused, and dev opens no file in it. A directory with a key file must be
// private and belong to the current user. It answers true for a directory
// that holds no key file yet, so the caller makes the key file first.
func claimDevDir(dir string) (bool, error) {
	info, err := os.Stat(dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		if mkErr := os.MkdirAll(dir, devDirPerm); mkErr != nil {
			return false, fmt.Errorf("make the data directory %s: %w", dir, mkErr)
		}

		return true, protectDevDir(dir)
	case err != nil:
		return false, fmt.Errorf("read the data directory %s: %w", dir, err)
	case !info.IsDir():
		return false, fmt.Errorf("%s: %w", dir, errDevNotDir)
	}

	names, err := devDirNames(dir)
	if err != nil {
		return false, err
	}

	switch {
	case len(names) == 0:
		return true, protectDevDir(dir)
	case !slices.Contains(names, devKeyName):
		return false, &refusalError{
			text:  fmt.Sprintf("%s: %s: dev refuses it. Set a new --data-dir.", dir, errDevForeign),
			cause: errDevForeign,
		}
	}

	return false, checkDevPath(dir, info, privateDir)
}

// inspectDevDir is claimDevDir without a write. It answers true for a data
// directory of dev that exists: it holds the key file, it is private and the
// current user owns it. It answers false and no error for a directory that
// does not exist or that holds files and no key file, and the error of
// claimDevDir for a directory that fails its checks.
func inspectDevDir(dir string) (bool, error) {
	info, err := os.Stat(dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("read the data directory %s: %w", dir, err)
	case !info.IsDir():
		return false, fmt.Errorf("%s: %w", dir, errDevNotDir)
	}

	names, err := devDirNames(dir)
	if err != nil {
		return false, err
	}

	if !slices.Contains(names, devKeyName) {
		return false, nil
	}

	if checkErr := checkDevPath(dir, info, privateDir); checkErr != nil {
		return false, checkErr
	}

	return true, nil
}

// devDirNames lists the entries of dir, without lost+found.
func devDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read the data directory %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if entry.Name() != lostAndFoundName {
			names = append(names, entry.Name())
		}
	}

	return names, nil
}

// protectDevDir sets mode 0700 on a directory that dev claims, after it
// checks that the current user owns the directory.
func protectDevDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("read the data directory %s: %w", dir, err)
	}

	if ownerErr := checkDevOwner(dir, info); ownerErr != nil {
		return ownerErr
	}

	if chmodErr := os.Chmod(dir, devDirPerm); chmodErr != nil {
		return fmt.Errorf("protect the data directory %s: %w", dir, chmodErr)
	}

	return nil
}

// pathClass says which rule checkDevPath applies to a path.
type pathClass int

const (
	// privateDir is the data directory: no access for the group or others.
	privateDir pathClass = iota
	// privateSecret is a file that holds a secret: no access for the group
	// or others.
	privateSecret
	// plainFile is any other file of dev: the group and others cannot write.
	plainFile
)

// checkDevPath refuses a path that the current user does not own, and a path
// that the group or others can access: any access for the data directory and
// for a file that holds a secret, write access for any other file. The error
// names the path, its mode or owner, and the one command that helps.
func checkDevPath(path string, info os.FileInfo, class pathClass) error {
	if err := checkDevOwner(path, info); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		// The mode bits of a Windows file are synthetic. The ACL check of
		// dev on Windows is not done yet.
		return nil
	}

	perm := info.Mode().Perm()

	switch class {
	case privateDir:
		if perm&devGroupOther != 0 {
			return fmt.Errorf("%s: %w (mode %04o): run chmod 700 %s", path, errDevPrivate, perm, path)
		}
	case privateSecret:
		if perm&devGroupOther != 0 {
			return fmt.Errorf("%s: %w (mode %04o): run chmod 600 %s", path, errDevPrivate, perm, path)
		}
	case plainFile:
		if perm&devGroupOtherW != 0 {
			return fmt.Errorf("%s: %w (mode %04o): run chmod 600 %s", path, errDevPrivate, perm, path)
		}
	default:
	}

	return nil
}

// checkDevFile applies checkDevPath to the file at path when it exists. A
// file that does not exist passes.
func checkDevFile(path string, class pathClass) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return checkDevPath(path, info, class)
}
