package launcher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrCopy reports a copy of the launcher binary that failed.
var ErrCopy = errors.New("copy of the launcher binary failed")

// ModeBinary is the mode of the copied launcher binary.
const ModeBinary = 0o755

// copyTemp is the name of the copy while it is written.
const copyTemp = "." + Binary + ".tmp"

// CopyBinary copies the file src to the directory dest as Binary with mode
// ModeBinary. It writes a new file through a root on dest, so a link in
// dest leads nowhere outside it, and renames it at the end, so dest never
// holds a part of the binary under the name Binary.
func CopyBinary(src, dest string) error {
	if !filepath.IsAbs(dest) {
		return fmt.Errorf("%w: the destination is not an absolute path", ErrCopy)
	}

	data, err := readSource(src)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("%w: open the destination: %w", ErrCopy, err)
	}

	if err = writeCopy(root, data); err != nil {
		var rmErr error
		if rm := root.Remove(copyTemp); rm != nil && !errors.Is(rm, os.ErrNotExist) {
			rmErr = fmt.Errorf("remove the part: %w", rm)
		}

		return fmt.Errorf("%w: %w", ErrCopy, errors.Join(err, rmErr, root.Close()))
	}

	if err = root.Close(); err != nil {
		return fmt.Errorf("%w: close the destination: %w", ErrCopy, err)
	}

	return nil
}

// writeCopy writes data to copyTemp in root with ModeBinary, and renames it
// to Binary.
func writeCopy(root *os.Root, data []byte) error {
	if err := root.Remove(copyTemp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove an old part: %w", err)
	}

	out, err := root.OpenFile(copyTemp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, ModeBinary)
	if err != nil {
		return fmt.Errorf("create the copy: %w", err)
	}

	_, err = out.Write(data)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("write the copy: %w", err)
	}

	if err = root.Chmod(copyTemp, ModeBinary); err != nil {
		return fmt.Errorf("set the mode of the copy: %w", err)
	}

	if err = root.Rename(copyTemp, Binary); err != nil {
		return fmt.Errorf("rename the copy: %w", err)
	}

	return nil
}

// maxBinary is the largest launcher binary that a copy reads.
const maxBinary = 512 << 20

// readSource reads the regular file src through a root on its directory.
func readSource(src string) ([]byte, error) {
	if !filepath.IsAbs(src) {
		return nil, fmt.Errorf("%w: the binary is not an absolute path", ErrCopy)
	}

	dir, err := os.OpenRoot(filepath.Dir(src))
	if err != nil {
		return nil, fmt.Errorf("%w: open the directory of the binary: %w", ErrCopy, err)
	}

	in, err := dir.Open(filepath.Base(src))
	if err != nil {
		return nil, fmt.Errorf("%w: open the binary: %w", ErrCopy, errors.Join(err, dir.Close()))
	}

	data, err := readRegular(in)

	if err = errors.Join(err, in.Close(), dir.Close()); err != nil {
		return nil, fmt.Errorf("%w: read the binary: %w", ErrCopy, err)
	}

	return data, nil
}

// Errors of the read of the binary.
var (
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("the file is too large")
)

// readRegular reads the regular file f, up to maxBinary bytes.
func readRegular(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}

	data, err := io.ReadAll(io.LimitReader(f, maxBinary+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	if len(data) > maxBinary {
		return nil, fmt.Errorf("%w: above %d bytes", errTooLarge, maxBinary)
	}

	return data, nil
}
