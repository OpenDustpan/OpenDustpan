package principal

// breakGlassSet is the set of working break-glass accounts of one scope, as
// one write reads it: the count of the accounts before the change, and the
// count that the change leaves. Only newBreakGlassSet makes one, so no rule
// sees a count that no read of the scope made.
type breakGlassSet struct {
	before int64
	after  int64
}

// newBreakGlassSet returns the set of one scope from the count of its working
// break-glass accounts before a change and the count that the change leaves.
// It is the only constructor of a breakGlassSet.
func newBreakGlassSet(before, after int64) breakGlassSet {
	return breakGlassSet{before: before, after: after}
}

// requireNotLast returns ErrNoWorkingBreakGlass when a disable or a delete
// leaves no enabled break-glass account in the scope of the set.
func (s breakGlassSet) requireNotLast() error {
	if s.after < FloorDisable {
		return ErrNoWorkingBreakGlass
	}

	return nil
}

// requireFloor returns ErrNoWorkingBreakGlass when a restore lowers the count
// of working accounts of the scope below FloorWorkingPaths. A change that does
// not lower the count is allowed, even below the floor, so that an
// installation can recover.
func (s breakGlassSet) requireFloor() error {
	if s.after < s.before && s.after < FloorWorkingPaths {
		return ErrNoWorkingBreakGlass
	}

	return nil
}

// CheckBreakGlassNotLast returns ErrNoWorkingBreakGlass when a disable or a
// delete leaves fewer than FloorDisable enabled break-glass accounts in the
// scope.
func CheckBreakGlassNotLast(after int64) error {
	return newBreakGlassSet(0, after).requireNotLast()
}

// CheckBreakGlassFloor returns ErrNoWorkingBreakGlass when a restore lowers the
// count of working break-glass accounts of one scope from before to after
// below FloorWorkingPaths.
func CheckBreakGlassFloor(before, after int64) error {
	return newBreakGlassSet(before, after).requireFloor()
}
