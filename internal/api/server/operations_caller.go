package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagCaller is the tag of the operations that serve the signed-in caller.
const tagCaller = "me"

// ownAccessBody describes the answer of GET /api/dustpan/v1/me/access. The
// route answers one of two documents: with object, the actions and grants on
// that object; with type, one page of the objects of that kind. The fields of
// the document that the query did not ask for are not in the answer.
type ownAccessBody struct {
	accessResponse
	accessObjectsResponse
}

// callerOperationLabels names the routes of the signed-in caller: its access,
// its grants and its own access tokens. The key is the pattern of the route.
// The lists take pageSize and pageToken. Only the token create takes the
// Idempotency-Key header.
func callerOperationLabels() map[string]apidesc.Label {
	page := func(what string) []apidesc.QueryParam {
		return []apidesc.QueryParam{
			apidesc.IntegerParam("pageSize", "The largest number of "+what+" on the page. The default is 50 and the maximum is 200."),
			apidesc.StringParam("pageToken", "The nextPageToken of the page before."),
		}
	}

	token := apidesc.Named[tokenResponse]("AccessToken")

	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, callerAccessPath): apidesc.Name("getOwnAccess", tagCaller).
			On(apidesc.KindCaller).
			WithQuery(append(page("objects"),
				apidesc.StringParam("object", "The object to answer for, for example @server or a namespace."),
				apidesc.StringParam("type", "The kind of the objects to list: namespace, collection, unit or principal."),
				apidesc.StringParam("action", "The action that the listed objects must allow."),
			)...).
			WithBodies(apidesc.NoBody, apidesc.Typed[ownAccessBody](
				"OwnAccess", apidesc.Named[grantResponse]("OwnAccessGrant"),
				apidesc.Named[missingActionResponse]("OwnMissingAction"),
				apidesc.Named[grantedByResponse]("OwnGrantedBy"),
				apidesc.Named[accessObjectResponse]("OwnAccessObject"),
			)),
		apidesc.Pattern(http.MethodGet, callerGrantsPath): apidesc.Name("listOwnGrants", tagCaller).
			On(apidesc.KindCaller).
			WithQuery(page("grants")...).
			WithBodies(apidesc.NoBody, apidesc.Typed[callerGrantsResponse](
				"ListOwnGrantsResponse", apidesc.Named[reviewRowResponse]("OwnGrant"),
				apidesc.Named[reviewViaResponse]("OwnGrantVia"),
			)),
		apidesc.Pattern(http.MethodGet, ownTokensPath): apidesc.Name("listOwnTokens", tagCaller).
			On(apidesc.KindCaller).
			WithQuery(append(page("tokens"),
				apidesc.StringParam("status", "Keep the tokens with this status: active, expiring or ended."),
			)...).
			WithBodies(apidesc.NoBody, apidesc.Typed[ownTokenListResponse]("ListOwnTokensResponse", token)),
		apidesc.Pattern(http.MethodPost, ownTokensPath): apidesc.Name("createOwnToken", tagCaller).
			WithStatus(http.StatusCreated).
			WithKey().
			On(apidesc.KindCaller).
			WithBodies(
				apidesc.Typed[createTokenRequest]("CreateOwnTokenRequest"),
				apidesc.Typed[newTokenResponse]("NewAccessToken"),
			),
		apidesc.Pattern(http.MethodDelete, ownTokenPath): apidesc.Name("deleteOwnToken", tagCaller).
			On(apidesc.KindCaller).
			WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodPost, ownTokenPath+"/rotate"): apidesc.Name("rotateOwnToken", tagCaller).
			On(apidesc.KindCaller).
			WithBodies(
				apidesc.Typed[rotateTokenRequest]("RotateOwnTokenRequest"),
				apidesc.Typed[rotationResponse]("OwnTokenRotation", token, apidesc.Named[newTokenResponse]("NewAccessToken")),
			),
	}
}
