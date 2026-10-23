package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// MaintenanceInMaintenance means that the installation is in maintenance mode.
	MaintenanceInMaintenance = "maintenance.in-maintenance"
)

// maintenanceSpecs are the codes of the installation state.
func maintenanceSpecs() []Spec {
	return []Spec{
		{Code: MaintenanceInMaintenance, Meaning: "the installation is in maintenance mode", Args: nil},
	}
}
