package storetest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"maps"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// portableBuffer is an in-memory file of PortableFiles.
type portableBuffer struct {
	*bytes.Buffer
}

// Close does nothing: the buffer keeps the bytes.
func (portableBuffer) Close() error {
	return nil
}

// PortableFiles exports s in the portable format into memory. It returns the
// table files by path, the summary and the descriptors. Store packages use it
// to build a stage input without a file system.
func PortableFiles(t *testing.T, s PortableStore) (map[string][]byte, backup.Summary, []backup.PortableFile) {
	t.Helper()

	buffers := make(map[string]*bytes.Buffer)

	sink := func(path string) (io.WriteCloser, error) {
		buf := &bytes.Buffer{}
		buffers[path] = buf

		return portableBuffer{Buffer: buf}, nil
	}

	sum, files, err := s.ExportPortable(t.Context(), sink)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	out := make(map[string][]byte, len(buffers))
	for path, buf := range buffers {
		out[path] = buf.Bytes()
	}

	return out, sum, files
}

// PortableOpener returns a backup.PortableOpener that reads the files of
// PortableFiles, or of EmptyPortableNamespaces.
func PortableOpener(files map[string][]byte) backup.PortableOpener {
	return func(path string) (io.ReadCloser, error) {
		data, ok := files[path]
		if !ok {
			return nil, fs.ErrNotExist
		}

		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

// EmptyPortableNamespaces returns a copy of a portable export whose namespaces
// file holds no rows. The descriptor and the summary count match the empty
// file, so only the foreign-key check of a stage can refuse it: the stacks of
// the export lose their namespace. No port writes an orphan, so the test
// edits the export by hand.
func EmptyPortableNamespaces(
	t *testing.T, files map[string][]byte, descs []backup.PortableFile, sum backup.Summary,
) (map[string][]byte, []backup.PortableFile, backup.Summary) {
	t.Helper()

	var empty bytes.Buffer

	zw := gzip.NewWriter(&empty)
	if err := zw.Close(); err != nil {
		t.Fatalf("write empty table file: %v", err)
	}

	data := empty.Bytes()
	digest := sha256.Sum256(data)

	out := maps.Clone(files)
	outDescs := make([]backup.PortableFile, 0, len(descs))

	for _, d := range descs {
		if d.Table == "namespaces" {
			out[d.Path] = data
			d.Rows = 0
			d.Size = int64(len(data))
			d.SHA256 = hex.EncodeToString(digest[:])
		}

		outDescs = append(outDescs, d)
	}

	sum.RowCounts = maps.Clone(sum.RowCounts)
	sum.RowCounts["namespaces"] = 0

	return out, outDescs, sum
}
