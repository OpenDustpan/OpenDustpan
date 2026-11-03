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
// keeps the data of one reset only: a new reset deletes the paths of the
// reset before.
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

	moved, err := moveDevData(dir)
	if err != nil {
		return err
	}

	app, err := newAppLog( //nolint:contextcheck // The log writers outlive this call; close bounds them.
		nil, slog.LevelInfo, "text", stderr, proc.lookup,
	)
	if err != nil {
		return err
	}

	defer closeAppLog(ctx, &app, stderr)

	app.logger.WarnContext(ctx, "dev reset moved the development data aside",
		slog.String("database", db), slog.Any("kept", moved))

	if remakeErr := remakeDevDatabase(ctx, db, app.logger, proc, moved); remakeErr != nil {
		return remakeErr
	}

	_, err = fmt.Fprintf(stderr, "dev reset: the old data is kept as %s. Run opendustpan dev to start.\n",
		filepath.Join(dir, devDBName+devResetSuffix))
	if err != nil {
		return fmt.Errorf("write the result: %w", err)
	}

	return nil
}

// checkDevReset answers the database of the data directory dir when dev reset
// can move it aside, and the refusal otherwise. Each check refuses alone:
// the directory must hold the key file of dev, no dev server may run on it,
// the database must be a regular file, and its stored kind must be dev.
func checkDevReset(ctx context.Context, dir string, proc devProcess) (string, error) {
	// Defense in depth: inspectDevDir follows a symbolic link, so this check
	// refuses a linked data directory on its own. The checks of the database
	// below do not rely on it.
	if dirInfo, statErr := os.Lstat(dir); statErr == nil && dirInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s: %w", dir, errDevResetLinkedDir)
	}

	claimed, err := inspectDevDir(dir)
	if err != nil {
		return "", err
	}

	if !claimed {
		return "", fmt.Errorf("%s: %w", dir, errDevResetNoDir)
	}

	if addr, ok := readDevAddr(dir); ok && devAddrLive(addr, proc.processStart) {
		return "", fmt.Errorf("%s: %w", dir, errDevResetLive)
	}

	db := filepath.Join(dir, devDBName)

	info, err := os.Lstat(db)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s: %w", dir, errDevResetNoDatabase)
	}

	if err != nil {
		return "", fmt.Errorf("read %s: %w", db, err)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s: %w", db, errDevResetNotFile)
	}

	// Defense in depth: a hard link passes the regular-file check above. This
	// check refuses a second name of the file on its own, and the kind check
	// below does not rely on it.
	if one, known := devOneLink(info); !known || !one {
		return "", fmt.Errorf("%s: %w", db, errDevResetHardLink)
	}

	if checkErr := checkDevPath(db, info, plainFile); checkErr != nil {
		return "", checkErr
	}

	if blobsErr := checkDevResetBlobs(filepath.Join(dir, devBlobsName)); blobsErr != nil {
		return "", blobsErr
	}

	// Defense in depth: StoredKind refuses a value that is not a known kind,
	// and this check refuses every kind but dev, also a kind that a later
	// change adds. The checks of the directory above do not decide it.
	kind, err := sqlite.StoredKind(ctx, db)
	if errors.Is(err, sqlite.ErrInUse) {
		return "", fmt.Errorf("%s: %w: %w", db, errDevResetInUse, err)
	}

	if err != nil {
		return "", fmt.Errorf("%s: %w: %w", db, errDevResetUnknownKind, err)
	}

	if kind != installation.KindDev {
		return "", fmt.Errorf("%s: opendustpan %s made this database: %w", db, commandOfKind(kind), errDevResetKind)
	}

	return db, nil
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

// moveDevData deletes the copies that the reset before kept, then moves each
// path of devResetMoves in dir aside, and answers the new paths. A path that
// does not exist is skipped. The names are fixed, and each path stays in dir.
func moveDevData(dir string) ([]string, error) {
	for _, move := range devResetMoves() {
		kept := filepath.Join(dir, move.to)
		if err := os.RemoveAll(kept); err != nil {
			return nil, fmt.Errorf("delete the data of the reset before, %s: %w", kept, err)
		}
	}

	var moved []string

	for _, move := range devResetMoves() {
		from, to := filepath.Join(dir, move.from), filepath.Join(dir, move.to)

		err := os.Rename(from, to)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return moved, fmt.Errorf("move %s aside: %w", from, err)
		}

		moved = append(moved, to)
	}

	return moved, nil
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
		text: "the schema changed: run " + reset + " to start again. The old data is kept as " +
			devDBName + devResetSuffix + " in the data directory: " + err.Error(),
		cause: err,
	}
}
