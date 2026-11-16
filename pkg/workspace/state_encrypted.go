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

	return s.clientEncryptedStreamed(ctx, sv)
}

// clientEncryptedStreamed reports whether the client encrypted the content of
// the finalized state version sv, from the sealed blob alone. It never reads
// the stored bit: a row of another version or of a restored backup is untrusted
// input, and a wrong bit would make this reader agree with it. The outputs read
// decides with this path, so its refusal to answer an empty list for a
// client-encrypted state does not depend on the row that the route read. The
// caller fails closed on each error.
func (s *Service) clientEncryptedStreamed(ctx context.Context, sv StateVersion) (bool, error) {
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
