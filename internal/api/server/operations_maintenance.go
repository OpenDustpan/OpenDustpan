package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagInstallation is the tag of the installation operations.
const tagInstallation = "installation"

// maintenanceOperationLabels names the maintenance, upgrade check, component
// and worker routes of the installation API. The key is the pattern of the
// route.
func maintenanceOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, maintenancePath): apidesc.Name("getMaintenance", tagInstallation).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[maintenanceStateResponse]("MaintenanceState")),
		apidesc.Pattern(http.MethodPost, maintenanceEnterPath): apidesc.Name("enterMaintenance", tagInstallation).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[enterMaintenanceRequest]("EnterMaintenanceRequest"),
				apidesc.Typed[enterMaintenanceResponse]("EnterMaintenanceResponse"),
			),
		apidesc.Pattern(http.MethodPost, maintenanceLeavePath): apidesc.Name("leaveMaintenance", tagInstallation).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[maintenanceStateResponse]("MaintenanceState")),
		apidesc.Pattern(http.MethodGet, upgradeCheckPath): apidesc.Name("getUpgradeCheck", tagInstallation).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[upgradeCheckResponse]("UpgradeCheck")),
		apidesc.Pattern(http.MethodGet, componentsPath): apidesc.Name("getComponents", tagInstallation).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[componentsResponse]("Components")),
		apidesc.Pattern(http.MethodGet, workerPath): apidesc.Name("getWorker", tagInstallation).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[workerResponse](
				"WorkerReport",
				apidesc.Named[unclaimedOperationJSON]("UnclaimedOperation"),
				apidesc.Named[dueScheduleJSON]("DueSchedule"),
			)),
	}
}
