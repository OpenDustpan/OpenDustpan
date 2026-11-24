package job

import "errors"

// ErrFreezeActive reports a create of a change freeze on a namespace that
// already has an active freeze. The first freeze stays unchanged. The
// store checks it in the transaction of the create.
var ErrFreezeActive = errors.New("a change freeze of the namespace is already active")
