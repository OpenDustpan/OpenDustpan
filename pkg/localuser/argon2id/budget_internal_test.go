package argon2id

// Waiting returns the count of Acquire calls that wait now. Only the tests use
// it, to start a waiter after another waiter is in the list.
func (b *Budget) Waiting() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.waiters)
}
