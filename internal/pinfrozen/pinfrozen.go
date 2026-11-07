// Package pinfrozen holds the rule for a branch before the first tag: the pin
// file of the API description does not change, and info.version of the
// description does not change. The rule does not apply after the first tag.
// The command in cmd/pinfrozen decides when the rule applies.
package pinfrozen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
)

// PinPath is the path of the pin file from the root of the repository.
const PinPath = "internal/api/apidesc/openapi.pin"

// DescriptionPath is the path of the checked-in API description.
const DescriptionPath = "internal/api/apidesc/openapi.json"

// ErrPinChanged reports a branch that changes the pin file before the first tag.
var ErrPinChanged = errors.New("the pin file changed before the first tag")

// ErrVersionChanged reports a branch that changes info.version of the
// description before the first tag.
var ErrVersionChanged = errors.New("info.version changed before the first tag")

// Reader returns the bytes of a path at one revision. For a path that the
// revision does not hold, it returns an error that wraps fs.ErrNotExist.
type Reader func(path string) ([]byte, error)

// Check returns the problems of the paths that a branch changed. It returns nil
// when no path breaks the rule. It fails closed: a description that cannot be
// read or parsed is a problem, not a pass.
func Check(changed []string, base, head Reader) error {
	var errs []error

	for _, path := range changed {
		switch path {
		case PinPath:
			errs = append(errs, fmt.Errorf("%w: %s: the pin does not change with a pull request until the first tag",
				ErrPinChanged, PinPath))
		case DescriptionPath:
			if err := checkVersion(base, head); err != nil {
				errs = append(errs, err)
			}
		default:
			// A path that the rule does not name has no problem here.
		}
	}

	return errors.Join(errs...)
}

// checkVersion compares info.version of the description at base and at head.
// A file that base does not hold has the empty version, and so does a file
// that head does not hold.
func checkVersion(base, head Reader) error {
	before, err := versionAt(base, DescriptionPath)
	if err != nil {
		return fmt.Errorf("read the base description: %w", err)
	}

	after, err := versionAt(head, DescriptionPath)
	if err != nil {
		return fmt.Errorf("read the head description: %w", err)
	}

	if before == after {
		return nil
	}

	return fmt.Errorf("%w: %s: info.version is %q at base and %q at head",
		ErrVersionChanged, DescriptionPath, before, after)
}

// versionAt returns info.version of the description at one revision. A missing
// file has the empty version. A file that is not valid JSON, or whose
// info.version is not a string, is an error.
func versionAt(read Reader, path string) (string, error) {
	raw, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	var doc description

	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}

	return doc.Info.Version, nil
}

// description is the part of the API description that the rule reads.
type description struct {
	Info info `json:"info"`
}

// info is the info object of the API description.
type info struct {
	Version string `json:"version"`
}
