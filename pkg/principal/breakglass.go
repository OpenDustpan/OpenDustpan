package principal

import "errors"

// lastBreakGlass names the refusal of a disable or a delete that would leave
// no enabled break-glass account. The store refuses that write with
// ErrNoWorkingBreakGlass, in the transaction of the write, and the Service
// gives its callers the narrower sentinel of the rule. A caller of the older
// sentinel still knows the refusal, because ErrLastBreakGlass wraps it.
func lastBreakGlass(err error) error {
	if errors.Is(err, ErrNoWorkingBreakGlass) {
		return ErrLastBreakGlass
	}

	return err
}
