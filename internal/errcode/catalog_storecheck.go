package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// StoreCheckInvalidTarget is a target that is not "blobs" or "backup".
	StoreCheckInvalidTarget = "storecheck.invalid-target"
	// StoreCheckNotFound is a check ID that has no row.
	StoreCheckNotFound = "storecheck.not-found"
	// StoreCheckNoTarget is a target that this server has not set.
	StoreCheckNoTarget = "storecheck.no-target"
	// StoreCheckRunning is a start while another store check runs.
	StoreCheckRunning = "storecheck.running"
	// StoreCheckCooldown is a start on a target whose last check ended less than 15 minutes ago.
	StoreCheckCooldown = "storecheck.cooldown"
	// StoreCheckRefused is a start while the installation is in maintenance mode or a restore runs.
	StoreCheckRefused = "storecheck.refused"
)

// storeCheckSpecs are the codes of the store checks.
func storeCheckSpecs() []Spec {
	return []Spec{
		{
			Code:    StoreCheckInvalidTarget,
			Meaning: "the target of a store check is not blobs or backup",
			Args:    nil,
		},
		{Code: StoreCheckNotFound, Meaning: "no store check has the ID", Args: nil},
		{Code: StoreCheckNoTarget, Meaning: "the server has not set the target of the store check", Args: nil},
		{Code: StoreCheckRunning, Meaning: "another store check runs, and one check runs at a time", Args: nil},
		{
			Code:    StoreCheckCooldown,
			Meaning: "a store check on the target ended less than 15 minutes ago",
			Args:    nil,
		},
		{
			Code:    StoreCheckRefused,
			Meaning: "a store check does not start in maintenance mode or while a restore runs",
			Args:    nil,
		},
	}
}
