package stack

import (
	"context"
	"errors"
)

// ErrNotAnOutput reports a ciphertext that is not the value of a secret root
// output of the latest checkpoint of a stack. The decrypt routes refuse it
// with 403 to a caller that may read the outputs of the stack and may not
// decrypt its other secrets, such as its config secrets.
var ErrNotAnOutput = errors.New("ciphertext is not a secret output of the latest checkpoint")

// OutputCiphertexts reads the ciphertexts that the root outputs of the latest
// checkpoint of a stack hold. The decrypt and batch-decrypt routes call it for
// the job token of another stack whose read of the outputs passes both
// checks of the sharing rules. Each ciphertext of such a request must be in
// the answer, or the route refuses the whole request with ErrNotAnOutput. A
// principal decrypts only with unit:decrypt, as before: unit:read-outputs and
// unit:read-secrets give a principal no decrypt, and the routes do not call
// this port for a principal. A Pulumi CLI that reads another stack with a
// stack reference decrypts its secret outputs on these routes: version
// 3.200.0 calls decrypt, and 3.250.0 and later call batch-decrypt.
//
// The routes read the latest checkpoint at the decrypt. When the producer
// saves a new checkpoint between the export and the decrypt, a ciphertext
// that only the older checkpoint holds is refused with 403. This fails
// closed. The next run of the job reads the new checkpoint.
//
// Only the Pulumi CLI has secret outputs as ciphertext of the producer. An
// OpenTofu output marked sensitive is plain text in the state, so this port
// belongs to the stack package and not to the tool-neutral output sharing
// package.
type OutputCiphertexts interface {
	// OutputCiphertexts returns the ciphertext of each secret value in the
	// root outputs of the latest checkpoint of ref, as the checkpoint holds
	// it, with no repeat and in no order. A secret inside an object or a list
	// of an output counts. A secret of a resource that is not the root stack
	// resource does not count, and a config secret does not count, because
	// the checkpoint does not hold it as an output. A stack with no secret
	// output gives an empty list and no error.
	//
	// It returns ErrNotFound for an unknown stack, ErrNoState when the stack
	// never saved state, and the errors of LatestState.Export for a
	// checkpoint that cannot be read (ErrCheckpointUnavailable,
	// ErrCheckpointMismatch). The caller refuses the decrypt on each error.
	OutputCiphertexts(ctx context.Context, ref Ref) ([]string, error)
}
