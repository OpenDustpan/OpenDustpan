package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// devResetCommand is the subcommand of dev that starts a data directory again
// on an empty database.
const devResetCommand = "reset"

// devResetSuffix ends the name of each path that dev reset keeps. dev reset
// keeps the data of one reset only: after the new database exists, a new reset
// deletes the paths of the reset before.
const devResetSuffix = ".reset.bak"

// devBlobsName is the blob directory of dev, next to the database.
const devBlobsName = "blobs"

// devResetSubject is the subject of the audit entry of a reset.
const devResetSubject = "opendustpan dev reset"

// Errors of dev reset. Each text names the cause and the one command that
// helps.
var (
	errDevResetNoDir = errors.New(
		"the directory is not a data directory of opendustpan dev: set --data-dir to the data directory of dev",
	)
	errDevResetNoDatabase = errors.New("the data directory holds no database: run opendustpan dev")
	errDevResetNotFile    = errors.New("the database is not a regular file: dev reset changes nothing")
	errDevResetLive       = errors.New(
		"a dev server runs on the data directory: stop it, then run opendustpan dev reset again",
	)
	errDevResetKind = errors.New(
		"dev reset works only on a database that opendustpan dev made: set a new --data-dir",
	)
	errDevResetUnknownKind = errors.New(
		"the installation kind of the database is not known: dev reset changes nothing",
	)
	errDevResetLinkedDir = errors.New(
		"the data directory is a symbolic link: set --data-dir to the directory itself",
	)
	errDevResetHardLink = errors.New(
		"the database has more than one name: dev reset changes nothing",
	)
	errDevResetBlobs = errors.New(
		"the blob directory is not a directory of the current user: dev reset changes nothing",
	)
	errDevResetOldCopy = errors.New(
		"a copy of a reset that did not finish is in the data directory. Move each copy out of the data directory to keep it, do not delete it. Then run opendustpan dev reset again",
	)
	errDevResetInUse = errors.New(
		"a server holds the database: stop it, then run opendustpan dev reset again",
	)
)

// devResetMove is one path of a data directory that dev reset moves aside:
// its name and the name of the copy.
type devResetMove struct {
	from string
	to   string
}

// devResetMoves answers the paths that dev reset moves aside, in the order of
// the move: the database, its write-ahead log files and the blob directory.
// The copy of each log file has the name that SQLite reads next to the copy
// of the database. Each name is fixed, so no path comes from the input.
func devResetMoves() []devResetMove {
	kept := devDBName + devResetSuffix

	return []devResetMove{
		{from: devDBName, to: kept},
		{from: devDBName + "-wal", to: kept + "-wal"},
		{from: devDBName + "-shm", to: kept + "-shm"},
		{from: devBlobsName, to: devBlobsName + devResetSuffix},
	}
}

// devReset runs dev reset: it moves the database and the blobs of a data
// directory of dev aside, makes the schema again on an empty database and
// records the reset. It refuses a database that a command other than dev
// made, and a database whose kind it cannot read.
func devReset(ctx context.Context, args []string, stderr io.Writer, proc devProcess) error {
	var dataDir string

	flags := flag.NewFlagSet("dev reset", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cli.DevByFlag("data-dir").String(flags, &dataDir)

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if flags.NArg() > 0 {
		return errDevArgs
	}

	envParent, _ := proc.lookup(cli.DevDataDirEnv)

	dir, err := devDirPath(proc.dirs(dataDir, envParent))
	if err != nil {
		return err
	}

	db, err := checkDevReset(ctx, dir, proc)
	if err != nil {
		return err
	}

	if oldErr := checkDevResetOldCopies(dir); oldErr != nil {
		return oldErr
	}

	moved, err := moveDevData(dir, os.Rename)
	if err != nil {
		return err
	}

	app, err := newAppLog( //nolint:contextcheck // The log writers outlive this call; close bounds them.
		nil, slog.LevelInfo, "text", stderr, sinkEnv{lookup: proc.lookup, ranges: nil},
	)
	if err != nil {
		return err
	}

	defer closeAppLog(ctx, &app, stderr)

	app.logger.WarnContext(ctx, "dev reset moved the development data aside",
		slog.String("database", db), slog.Any("kept", moved))

	return finishDevReset(ctx, devFinish{
		dir: dir, db: db, moved: moved, stderr: stderr, logger: app.logger, proc: proc,
	}, remakeDevDatabase)
}

// devRemake makes the new database at db. devReset passes remakeDevDatabase.
type devRemake func(ctx context.Context, db string, logger *slog.Logger, proc devProcess, moved []string) error

// devFinish holds the values that devReset passes to finishDevReset.
type devFinish struct {
	dir    string
	db     string
	moved  []string
	stderr io.Writer
	logger *slog.Logger
	proc   devProcess
}

// finishDevReset makes the new database with remake, deletes the copies of the
// reset before only after that succeeds, and writes the result. When the remake
// fails, the copies stay, and the error names them.
func finishDevReset(ctx context.Context, f devFinish, remake devRemake) error {
	if remakeErr := remake(ctx, f.db, f.logger, f.proc, f.moved); remakeErr != nil {
		return keptCopiesError(f.dir, remakeErr)
	}

	if dropErr := dropOldDevCopies(f.dir); dropErr != nil {
		return dropErr
	}

	_, err := fmt.Fprintf(f.stderr, "dev reset: the old data is kept as %s. Run opendustpan dev to start.\n",
		filepath.Join(f.dir, devDBName+devResetSuffix))
	if err != nil {
		return fmt.Errorf("write the result: %w", err)
	}

	return nil
}

// checkDevReset answers the database of the data directory dir when dev reset
// can move it aside, and the refusal otherwise. Each check refuses alone:
// the directory must be a real directory with the key file of dev and no
// live dev server, the database must be a regular file with one name, the
// blob directory must be private, and the stored kind must be dev.
func checkDevReset(ctx context.Context, dir string, proc devProcess) (string, error) {
	if err := checkDevResetDir(dir, proc); err != nil {
		return "", err
	}

	db := filepath.Join(dir, devDBName)

	if err := checkDevResetDatabase(dir, db); err != nil {
		return "", err
	}

	if err := checkDevResetBlobs(filepath.Join(dir, devBlobsName)); err != nil {
		return "", err
	}

	if err := checkDevResetKind(ctx, db); err != nil {
		return "", err
	}

	return db, nil
}

// checkDevResetDir refuses a data directory that is a symbolic link, that
// holds no key file of dev, or that a live dev server uses.
func checkDevResetDir(dir string, proc devProcess) error {
	return checkDevResetDirWithLstat(dir, proc, os.Lstat)
}

// checkDevResetDirWithLstat is checkDevResetDir with the link status function
// as a parameter, so that a test can make Lstat fail.
func checkDevResetDirWithLstat(dir string, proc devProcess, lstat func(string) (fs.FileInfo, error)) error {
	// Defense in depth: inspectDevDir follows a symbolic link, so this check
	// refuses a linked data directory on its own. The checks of the database
	// do not rely on it.
	// Fail closed: a directory that Lstat cannot read is refused, and only a
	// missing directory passes on to the checks below.
	info, err := lstat(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: %w", dir, err)
	}

	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s: %w", dir, errDevResetLinkedDir)
	}

	claimed, err := inspectDevDir(dir)
	if err != nil {
		return err
	}

	if !claimed {
		return fmt.Errorf("%s: %w", dir, errDevResetNoDir)
	}

	if addr, ok := readDevAddr(dir); ok && devAddrLive(addr, proc.processStart) {
		return fmt.Errorf("%s: %w", dir, errDevResetLive)
	}

	return nil
}

// checkDevResetDatabase refuses a database at db that does not exist, that is
// not a regular file, that has a second name, or that others can write.
func checkDevResetDatabase(dir, db string) error {
	info, err := os.Lstat(db)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: %w", dir, errDevResetNoDatabase)
	}

	if err != nil {
		return fmt.Errorf("read %s: %w", db, err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", db, errDevResetNotFile)
	}

	// Defense in depth: a hard link passes the regular-file check above. This
	// check refuses a second name of the file on its own, and the kind check
	// does not rely on it.
	if !devOneLink(info) {
		return fmt.Errorf("%s: %w", db, errDevResetHardLink)
	}

	return checkDevPath(db, info, plainFile)
}

// checkDevResetKind refuses a database whose stored kind is not dev, also a
// database that another process holds or whose kind cannot be read.
func checkDevResetKind(ctx context.Context, db string) error {
	// Defense in depth: StoredKind refuses a value that is not a known kind,
	// and this check refuses every kind but dev, also a kind that a later
	// change adds. The checks of the paths do not decide it.
	kind, err := sqlite.StoredKind(ctx, db)
	if errors.Is(err, sqlite.ErrInUse) {
		return fmt.Errorf("%s: %w: %w", db, errDevResetInUse, err)
	}

	if err != nil {
		return fmt.Errorf("%s: %w: %w", db, errDevResetUnknownKind, err)
	}

	if kind != installation.KindDev {
		return fmt.Errorf("%s: opendustpan %s made this database: %w", db, commandOfKind(kind), errDevResetKind)
	}

	return nil
}

// checkDevResetBlobs refuses the blob directory at path unless it is a real
// directory of the current user that only that user can access, or does not
// exist. A symbolic link fails: Lstat does not follow it.
func checkDevResetBlobs(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s: %w", path, errDevResetBlobs)
	}

	if checkErr := checkDevPath(path, info, privateDir); checkErr != nil {
		return fmt.Errorf("%w: %w", errDevResetBlobs, checkErr)
	}

	return nil
}

// devResetOldSuffix ends the name of a copy of the reset before while dev reset
// needs it. dev reset deletes these copies only after the new database exists.
const devResetOldSuffix = ".old"

// moveDevData keeps the copies of the reset before under the suffix
// devResetOldSuffix, then moves each path of devResetMoves in dir aside with
// rename, and answers the new paths. A path that does not exist is skipped.
// The names are fixed, and each path stays in dir. An error names the paths
// that moved and the copies of the reset before, so that no data is lost.
func moveDevData(dir string, rename func(from, to string) error) ([]string, error) {
	var copies []string

	for _, move := range devResetMoves() {
		to := filepath.Join(dir, move.to)
		old := to + devResetOldSuffix

		_, statErr := os.Lstat(to)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}

		if statErr != nil {
			return nil, fmt.Errorf("read %s: %w", to, statErr)
		}

		// A copy under the old suffix is left by a reset that stopped before it
		// finished. The copy of the reset before is at to, and rename keeps it.
		if err := os.RemoveAll(old); err != nil {
			return nil, fmt.Errorf("delete the stale copy %s: %w", old, err)
		}

		if err := rename(to, old); err != nil {
			return nil, moveDevDataError(fmt.Errorf("keep the copy of the reset before: %w", err), to, nil, copies)
		}

		copies = append(copies, old)
	}

	var moved []string

	for _, move := range devResetMoves() {
		from, to := filepath.Join(dir, move.from), filepath.Join(dir, move.to)

		err := rename(from, to)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return moved, moveDevDataError(fmt.Errorf("move %s aside: %w", from, err), from, moved, copies)
		}

		moved = append(moved, to)
	}

	return moved, nil
}

// moveDevDataError names the path that failed, the paths already moved and the
// copies of the reset before, and the one command that helps.
func moveDevDataError(cause error, failed string, moved, copies []string) error {
	return fmt.Errorf("%w. Failed path: %s. Moved: %s. Copies of the reset before: %s. "+
		"Move each copy out of the data directory to keep it, do not delete it. Then run opendustpan dev reset again",
		cause, failed, joinOrNone(moved), joinOrNone(copies))
}

// joinOrNone joins paths with a comma, or answers none for no path.
func joinOrNone(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}

	return strings.Join(paths, ", ")
}

// checkDevResetOldCopies refuses a data directory that holds a copy under
// devResetOldSuffix. Such a copy is left by a reset that did not finish, and
// the reset would move it aside and delete it, so the last good backup would be
// lost.
func checkDevResetOldCopies(dir string) error {
	// Defense in depth: moveDevData replaces a stale copy under the suffix. This
	// check refuses the directory before any move, so the copy is never deleted
	// by a rerun on its own.
	if paths := devResetOldPaths(dir); len(paths) > 0 {
		return fmt.Errorf("%s: %w", strings.Join(paths, ", "), errDevResetOldCopy)
	}

	return nil
}

// dropOldDevCopies deletes the copies of the reset before that moveDevData
// kept under devResetOldSuffix. The caller calls it only after the new database
// exists.
func dropOldDevCopies(dir string) error {
	for _, move := range devResetMoves() {
		old := filepath.Join(dir, move.to+devResetOldSuffix)
		if err := os.RemoveAll(old); err != nil {
			return fmt.Errorf("delete the copy of the reset before, %s: %w", old, err)
		}
	}

	return nil
}

// devResetOldPaths answers the copies of the reset before that stay in dir
// under devResetOldSuffix.
func devResetOldPaths(dir string) []string {
	var paths []string

	for _, move := range devResetMoves() {
		old := filepath.Join(dir, move.to+devResetOldSuffix)

		_, err := os.Lstat(old)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			paths = append(paths, old)
		}
	}

	return paths
}

// keptCopiesError adds the names of the copies of the reset before to the error
// of a failed remake. Those copies stay in dir.
func keptCopiesError(dir string, err error) error {
	paths := devResetOldPaths(dir)
	if len(paths) == 0 {
		return err
	}

	return fmt.Errorf("%w. The copies of the reset before stay at: %s", err, strings.Join(paths, ", "))
}

// remakeDevDatabase makes the schema of the binary on a new database at db,
// claims the kind dev and writes the audit entry of the reset.
func remakeDevDatabase(ctx context.Context, db string, logger *slog.Logger, proc devProcess, moved []string) error {
	st, err := openStore(ctx, "sqlite:"+db, logger, openMigrate, installation.KindDev)
	if err != nil {
		return err
	}

	defer st.close()

	if _, err := st.ClaimKind(ctx, installation.KindDev); err != nil {
		return fmt.Errorf("claim the installation kind: %w", err)
	}

	changes := []audit.Change{
		kindChange(installation.KindDev),
		{Key: "dev-reset-kept", Old: nil, New: strings.Join(moved, ", "), Layer: "default", TakesEffect: ""},
	}

	entry := cli.SettingsEntry(audit.ChannelStartup, devResetSubject, proc.now(), changes)
	entry.Reason = "dev reset moved the development data aside and made the schema again"

	if _, err := st.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("write the audit entry of the reset: %w", err)
	}

	return nil
}

// schemaRefusal turns the refusal of a database whose schema differs from the
// schema of the binary into the refusal of command, with the one step that
// helps: dev names dev reset, and serve names a backup or a new database.
// serve never offers a reset. It answers every other error unchanged.
func schemaRefusal(err error, command, dataDir string) error {
	if !errors.Is(err, schema.ErrSchemaDiverged) && !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		return err
	}

	if command != commandDev {
		return &refusalError{
			text: "the schema of this database differs from the schema of this binary: " +
				"restore a backup of this build, or set --database to a new database: " + err.Error(),
			cause: err,
		}
	}

	reset := "opendustpan dev reset"
	if dataDir != "" {
		reset += " --data-dir " + shellQuote(dataDir)
	}

	return &refusalError{
		text: "the schema changed: run " + reset + " to start again. That command moves the old data aside as " +
			devDBName + devResetSuffix + " in the data directory: " + err.Error(),
		cause: err,
	}
}
