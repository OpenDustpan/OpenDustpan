package backup_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"opendustpan.org/opendustpan/pkg/backup"
)

// sqlPartTable is the table whose file holds the test SQL part.
const sqlPartTable = "namespaces"

// sqlPartKey returns the target key of the test SQL part of backup id.
func sqlPartKey(id backup.ID) string {
	return "backups/" + id.String() + "/" + backup.PortablePath(sqlPartTable)
}

// sqlPartAggregate returns the size and the SHA-256 that a manifest keeps for
// its SQL part: the sum of the file sizes, and the SHA-256 of the descriptors
// as JSON. The writer computes the same values (ADR 42, decision 4).
func sqlPartAggregate(files []backup.PortableFile) (int64, string) {
	var size int64

	for _, file := range files {
		size += file.Size
	}

	raw, err := json.Marshal(files)
	if err != nil {
		panic(err)
	}

	sum := sha256.Sum256(raw)

	return size, hex.EncodeToString(sum[:])
}

// sqlPartAllFiles returns the descriptors of a whole SQL part: one file for each
// table of Tables, each holding data. A restore reads every table, so a
// restore fixture names them all.
func sqlPartAllFiles(data []byte) []backup.PortableFile {
	files := make([]backup.PortableFile, 0, len(backup.Tables))

	for _, table := range backup.Tables {
		digest := sha256.Sum256(data)

		files = append(files, backup.PortableFile{
			Table:   table,
			Path:    backup.PortablePath(table),
			SHA256:  hex.EncodeToString(digest[:]),
			Columns: nil,
			Rows:    0,
			Size:    int64(len(data)),
		})
	}

	return files
}

// sqlPartAllKey returns the target key of one table file of backup id.
func sqlPartAllKey(id backup.ID, table string) string {
	return "backups/" + id.String() + "/" + backup.PortablePath(table)
}
