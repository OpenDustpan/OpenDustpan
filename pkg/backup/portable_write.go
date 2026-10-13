package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// PortableReader reads the rows of one table in the order of its OrderBy
// columns and calls emit once for each row, with one value for each column in
// the order of the columns. A dialect reader maps its storage form to the values
// that TableWriter accepts. A failed emit stops the read.
type PortableReader func(ctx context.Context, table PortableTable, emit func(values []any) error) error

// WritePortable writes one file for each table of PortableTables through sink,
// reading the rows with read. It returns the file descriptors in the order of
// the tables and the row count of each table. It stops at the first failure
// and closes the file it has open.
func WritePortable(
	ctx context.Context, sink PortableSink, read PortableReader,
) ([]PortableFile, map[string]int64, error) {
	tables := PortableTables()
	files := make([]PortableFile, 0, len(tables))
	counts := make(map[string]int64, len(tables))

	for _, table := range tables {
		file, err := writePortableTable(ctx, sink, read, table)
		if err != nil {
			return nil, nil, err
		}

		files = append(files, file)
		counts[table.Table] = file.Rows
	}

	return files, counts, nil
}

// writePortableTable writes the file of one table.
func writePortableTable(
	ctx context.Context, sink PortableSink, read PortableReader, table PortableTable,
) (PortableFile, error) {
	out, err := sink(PortablePath(table.Table))
	if err != nil {
		return PortableFile{}, fmt.Errorf("open file of table %q: %w", table.Table, err)
	}

	tw, err := NewTableWriter(out, table)
	if err != nil {
		return PortableFile{}, errors.Join(err, closeSink(out, err))
	}

	if readErr := read(ctx, table, tw.WriteRow); readErr != nil {
		readErr = fmt.Errorf("read table %q: %w", table.Table, readErr)

		return PortableFile{}, errors.Join(readErr, closeSink(out, readErr))
	}

	file, err := tw.Close()
	if err != nil {
		return PortableFile{}, errors.Join(err, closeSink(out, err))
	}

	if closeErr := out.Close(); closeErr != nil {
		return PortableFile{}, fmt.Errorf("close file of table %q: %w", table.Table, closeErr)
	}

	return file, nil
}

// abortSink is a sink that drops its bytes when the export fails, so a partial
// file never completes in the target. A sink without it is closed instead.
type abortSink interface {
	Abort(err error)
}

// closeSink closes a sink after a failure. The first failure is the one that
// the caller reports, so a close error here is only joined, never returned alone.
func closeSink(out io.WriteCloser, cause error) error {
	if aborter, ok := out.(abortSink); ok {
		aborter.Abort(cause)

		return nil
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("close file after failure: %w", err)
	}

	return nil
}
