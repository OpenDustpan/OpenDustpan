package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// MaintenanceActiveWork means that updates or runs are active, so the installation cannot enter maintenance mode.
	MaintenanceActiveWork = "maintenance.active-work"
	// MaintenanceHeld means that maintenance mode stays until the change of the audit chain ends.
	MaintenanceHeld = "maintenance.held"
	// MaintenanceInMaintenance means that the installation is in maintenance mode.
	MaintenanceInMaintenance = "maintenance.in-maintenance"
	// MaintenanceInvalidState means that the installation state is neither normal nor maintenance.
	MaintenanceInvalidState = "maintenance.invalid-state"
	// MaintenanceUnavailable means that the server cannot read the installation state.
	MaintenanceUnavailable = "maintenance.unavailable"
)

// maintenanceSpecs are the codes of the installation state.
func maintenanceSpecs() []Spec {
	return []Spec{
		{
			Code:    MaintenanceActiveWork,
			Meaning: "updates or runs are active, so the installation cannot enter maintenance mode",
			Args: []Arg{
				{Name: "workCount", Kind: KindQuantity},
				{Name: "canceledCount", Kind: KindQuantity},
			},
		},
		{Code: MaintenanceHeld, Meaning: "maintenance mode stays until the change of the audit chain ends", Args: nil},
		{Code: MaintenanceInMaintenance, Meaning: "the installation is in maintenance mode", Args: nil},
		{
			Code:    MaintenanceInvalidState,
			Meaning: "the installation state is neither normal nor maintenance",
			Args:    nil,
		},
		{Code: MaintenanceUnavailable, Meaning: "the server cannot read the installation state", Args: nil},
	}
}
