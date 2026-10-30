package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagStoreChecks is the tag of the store check operations.
const tagStoreChecks = "store-checks"

// storeCheckOperationLabels names the store check routes. The key is the
// pattern of the route. The start answers 202: the check runs in the
// background.
func storeCheckOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodPost, storeChecksPath): apidesc.Name("startStoreCheck", tagStoreChecks).
			WithStatus(http.StatusAccepted).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[startStoreCheckRequest]("StartStoreCheckRequest"),
				apidesc.Typed[startStoreCheckResponse]("StartStoreCheckResponse"),
			),
		apidesc.Pattern(http.MethodGet, storeChecksPath): apidesc.Name("listStoreChecks", tagStoreChecks).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[listStoreChecksResponse](
				"ListStoreChecksResponse", apidesc.Named[storeCheckJSON]("StoreCheck"),
				apidesc.Named[storeCheckReportJSON]("StoreCheckReport"),
				apidesc.Named[storeCheckCaseJSON]("StoreCheckCase"),
				apidesc.Named[storeCheckLatencyJSON]("StoreCheckLatency"),
			)),
		apidesc.Pattern(http.MethodGet, storeCheckPath): apidesc.Name("getStoreCheck", tagStoreChecks).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[storeCheckJSON](
				"StoreCheck", apidesc.Named[storeCheckReportJSON]("StoreCheckReport"),
				apidesc.Named[storeCheckCaseJSON]("StoreCheckCase"),
				apidesc.Named[storeCheckLatencyJSON]("StoreCheckLatency"),
			)),
	}
}
