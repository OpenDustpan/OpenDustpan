package workspace

import (
	"context"
	"fmt"
)

// StateClientEncrypted reports whether the client encrypted the content of
// the finalized state version id. It opens the sealed blob like Open and
// streams the top-level members of the document without a tree, so its memory
// does not grow with the size of the state. A state that is not a JSON object
// fails with ErrInvalidState, and an unknown or pending version fails with
// ErrStateVersionNotFound. The caller fails closed on each error.
func (s *Service) StateClientEncrypted(ctx context.Context, id StateVersionID) (bool, error) {
	sv, err := s.ReadState(ctx, id)
	if err != nil {
		return false, err
	}

	content, err := s.Open(ctx, sv)
	if err != nil {
		return false, err
	}

	meta, readErr := readStateMeta(content, nil)
	closeErr := content.Close()

	if readErr != nil {
		return false, fmt.Errorf("read the state marker of %s: %w", id, readErr)
	}

	if closeErr != nil {
		return false, fmt.Errorf("close state %s: %w", id, closeErr)
	}

	return meta.ClientEncrypted, nil
}
