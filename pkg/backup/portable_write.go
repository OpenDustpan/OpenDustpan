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
		return PortableFile{}, errors.Join(err, closeSink(out))
	}

	if readErr := read(ctx, table, tw.WriteRow); readErr != nil {
		return PortableFile{}, errors.Join(fmt.Errorf("read table %q: %w", table.Table, readErr), closeSink(out))
	}

	file, err := tw.Close()
	if err != nil {
		return PortableFile{}, errors.Join(err, closeSink(out))
	}

	if closeErr := out.Close(); closeErr != nil {
		return PortableFile{}, fmt.Errorf("close file of table %q: %w", table.Table, closeErr)
	}

	return file, nil
}

// closeSink closes a sink after a failure. The first failure is the one that
// the caller reports, so a close error here is only joined, never returned alone.
func closeSink(out io.WriteCloser) error {
	if err := out.Close(); err != nil {
		return fmt.Errorf("close file after failure: %w", err)
	}

	return nil
}

// PortableManifest is the manifest of a portable backup (ADR 42, decision 4).
// It carries the same summary as the native manifest, the format number 2 and
// the sqlFormat name, and one descriptor for each table file. The backup adds
// the blob references and the copy results when it writes the manifest.
type PortableManifest struct {
	Dialect       Dialect          `json:"dialect"`
	SQLFormat     string           `json:"sqlFormat"`
	HomeRegions   []string         `json:"homeRegions"`
	WrapperIDs    []string         `json:"wrapperIds"`
	RowCounts     map[string]int64 `json:"rowCounts"`
	Files         []PortableFile   `json:"files"`
	Format        int              `json:"format"`
	SchemaVersion int              `json:"schemaVersion"`
}

// NewPortableManifest returns the manifest of a portable backup of sum with the
// file descriptors files.
func NewPortableManifest(sum Summary, files []PortableFile) PortableManifest {
	return PortableManifest{
		Dialect:       sum.Dialect,
		SQLFormat:     PortableFormat,
		HomeRegions:   sum.HomeRegions,
		WrapperIDs:    sum.WrapperIDs,
		RowCounts:     sum.RowCounts,
		Files:         files,
		Format:        PortableManifestFormat,
		SchemaVersion: sum.SchemaVersion,
	}
}
