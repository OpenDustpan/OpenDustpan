package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// RequestBodyInvalid means that the request body is not readable or is not one JSON object with known fields.
	RequestBodyInvalid = "request.body-invalid"
	// RequestContentTypeUnsupported means that the content type of the request is not one that the route takes.
	RequestContentTypeUnsupported = "request.content-type-unsupported"
	// RequestIdempotencyKeyInProgress means that a request with the same Idempotency-Key is in progress.
	RequestIdempotencyKeyInProgress = "request.idempotency-key-in-progress"
	// RequestIdempotencyKeyInvalid means that the Idempotency-Key header is not valid.
	RequestIdempotencyKeyInvalid = "request.idempotency-key-invalid"
	// RequestIdempotencyKeyReplayedSecret means that the request replays a request whose answer holds a secret, which
	// the server does not keep.
	RequestIdempotencyKeyReplayedSecret = "request.idempotency-key-replayed-secret"
	// RequestIdempotencyKeyLost means that a newer request took the Idempotency-Key before this request ended.
	RequestIdempotencyKeyLost = "request.idempotency-key-lost"
	// RequestIdempotencyKeyRequired means that the route needs the Idempotency-Key header.
	RequestIdempotencyKeyRequired = "request.idempotency-key-required"
	// RequestIdempotencyKeyReused means that the Idempotency-Key was used for another request.
	RequestIdempotencyKeyReused = "request.idempotency-key-reused"
	// RequestIdempotencyKeyUnavailable means that the Idempotency-Key header is not available on this server.
	RequestIdempotencyKeyUnavailable = "request.idempotency-key-unavailable"
	// RequestIdempotencyKeyUnsupported means that the route does not take the Idempotency-Key header.
	RequestIdempotencyKeyUnsupported = "request.idempotency-key-unsupported"
	// RequestInvalidObject means that a namespace or unit reference in the request is malformed.
	RequestInvalidObject = "request.invalid-object"
	// RequestInvalidPath means that a value in the path of the request is not valid.
	RequestInvalidPath = "request.invalid-path"
	// RequestInvalidQuery means that a query parameter of the request is not valid.
	RequestInvalidQuery = "request.invalid-query"
	// RequestPageSizeInvalid means that the page size is not valid.
	RequestPageSizeInvalid = "request.page-size-invalid"
	// RequestPageTokenInvalid means that the page token is not valid.
	RequestPageTokenInvalid = "request.page-token-invalid"
	// RequestPreconditionFailed means that the object changed since the version of the If-Match header.
	RequestPreconditionFailed = "request.precondition-failed"
	// RequestPreconditionRequired means that the route needs the If-Match header.
	RequestPreconditionRequired = "request.precondition-required"
)

// requestSpecs are the codes of a request that is not valid as a request,
// before any domain rule.
func requestSpecs() []Spec {
	return []Spec{
		{
			Code:    RequestBodyInvalid,
			Meaning: "the request body is not readable or is not one JSON object with known fields",
			Args:    nil,
		},
		{
			Code:    RequestContentTypeUnsupported,
			Meaning: "the content type of the request is not one that the route takes",
			Args:    nil,
		},
		{
			Code:    RequestIdempotencyKeyInProgress,
			Meaning: "a request with the same Idempotency-Key is in progress",
			Args:    nil,
		},
		{Code: RequestIdempotencyKeyInvalid, Meaning: "the Idempotency-Key header is not valid", Args: nil},
		{
			Code:    RequestIdempotencyKeyReplayedSecret,
			Meaning: "the request replays a request whose answer holds a secret, which the server does not keep",
			Args:    []Arg{{Name: "objectID", Kind: KindIdentifier}},
		},
		{
			Code:    RequestIdempotencyKeyLost,
			Meaning: "a newer request took the Idempotency-Key before this request ended",
			Args:    nil,
		},
		{Code: RequestIdempotencyKeyRequired, Meaning: "the route needs the Idempotency-Key header", Args: nil},
		{Code: RequestIdempotencyKeyReused, Meaning: "the Idempotency-Key was used for another request", Args: nil},
		{
			Code:    RequestIdempotencyKeyUnavailable,
			Meaning: "the Idempotency-Key header is not available on this server",
			Args:    nil,
		},
		{
			Code:    RequestIdempotencyKeyUnsupported,
			Meaning: "the route does not take the Idempotency-Key header",
			Args:    nil,
		},
		{Code: RequestInvalidObject, Meaning: "a namespace or unit reference in the request is malformed", Args: nil},
		{Code: RequestInvalidPath, Meaning: "a value in the path of the request is not valid", Args: nil},
		{Code: RequestInvalidQuery, Meaning: "a query parameter of the request is not valid", Args: nil},
		{Code: RequestPageSizeInvalid, Meaning: "the page size is not valid", Args: nil},
		{Code: RequestPageTokenInvalid, Meaning: "the page token is not valid", Args: nil},
		{
			Code:    RequestPreconditionFailed,
			Meaning: "the object changed since the version of the If-Match header",
			Args:    nil,
		},
		{Code: RequestPreconditionRequired, Meaning: "the route needs the If-Match header", Args: nil},
	}
}
