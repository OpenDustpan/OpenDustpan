package server

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/ident"
)

// errHiddenText reports a text field of a request body that holds a control,
// format or separator character. classify maps it to 400.
var errHiddenText = errors.New("invalid text")

// refuseHidden returns an error that wraps errHiddenText when s holds a
// hidden character (see ident.IsHiddenRune). The message names the field and
// the refused character.
func refuseHidden(field, s string) error {
	r, found := ident.FirstHidden(s)
	if !found {
		return nil
	}

	return fmt.Errorf("%w: %s holds the control, format or separator character %U", errHiddenText, field, r)
}

// refuseHiddenPtr is refuseHidden for an optional field. A field that the
// request leaves out is valid.
func refuseHiddenPtr(field string, s *string) error {
	if s == nil {
		return nil
	}

	return refuseHidden(field, *s)
}
