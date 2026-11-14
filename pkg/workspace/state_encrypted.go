package workspace

import (
	"context"
	"fmt"
)

// StateClientEncrypted reports whether the client encrypted the content of
// the finalized state version id. It reads the state version row first: when
// the row holds the client-encryption bit, it answers from the row and reads
// no blob. A version stored before the server kept the bit reports unknown, so
// it opens the sealed blob like Open and streams the top-level members of the
// document without a tree; its memory does not grow with the size of the
// state. A state that is not a JSON object fails with ErrInvalidState, and an
// unknown or pending version fails with ErrStateVersionNotFound. The caller
// fails closed on each error.
func (s *Service) StateClientEncrypted(ctx context.Context, id StateVersionID) (bool, error) {
	sv, err := s.ReadState(ctx, id)
	if err != nil {
		return false, err
	}

	return s.ClientEncryptedOf(ctx, sv)
}

// ClientEncryptedOf reports whether the client encrypted the content of the
// finalized state version sv. When sv holds the bit, it answers from the row
// and reads no blob. An unknown bit, the bit of a version stored before the
// server kept it, streams the sealed blob to decide. The caller fails closed
// on each error.
func (s *Service) ClientEncryptedOf(ctx context.Context, sv StateVersion) (bool, error) {
	if sv.ClientEncrypted.Known() {
		return sv.ClientEncrypted.Encrypted(), nil
	}

	content, err := s.Open(ctx, sv)
	if err != nil {
		return false, err
	}

	meta, readErr := readStateMeta(content, nil)
	closeErr := content.Close()

	if readErr != nil {
		return false, fmt.Errorf("read the state marker of %s: %w", sv.ID, readErr)
	}

	if closeErr != nil {
		return false, fmt.Errorf("close state %s: %w", sv.ID, closeErr)
	}

	return meta.ClientEncrypted, nil
}
