package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// firstAdministratorOperationLabels names the routes of the first
// administrator in the description. The key is the pattern of the route. No
// route takes the Idempotency-Key header, and no route takes a query
// parameter.
func firstAdministratorOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, firstAdministratorPath): apidesc.Name(
			"getFirstAdministrator", tagFirstAdministrator,
		).WithBodies(apidesc.NoBody, apidesc.Typed[firstAdministratorStateResponse](
			"FirstAdministratorState", apidesc.Named[firstAdministratorOfferState]("FirstAdministratorOffer"),
		)),
		apidesc.Pattern(http.MethodPost, firstAdministratorPath): apidesc.Name(
			"offerFirstAdministrator", tagFirstAdministrator,
		).WithStatus(http.StatusCreated).WithBodies(
			apidesc.Typed[offerFirstAdministratorRequest]("OfferFirstAdministratorRequest"),
			apidesc.Typed[offerFirstAdministratorResponse]("FirstAdministratorOffered"),
		),
		apidesc.Pattern(http.MethodPost, firstAdministratorRedeemPath): apidesc.Name(
			"redeemFirstAdministrator", tagFirstAdministrator,
		).On(apidesc.KindCaller).WithBodies(
			apidesc.Typed[redeemFirstAdministratorRequest]("RedeemFirstAdministratorRequest"),
			apidesc.Typed[redeemFirstAdministratorResponse]("FirstAdministratorRedeemed"),
		),
	}
}
