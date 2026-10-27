package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// SinkAccessToken means that the access token of the notification destination is not valid for its platform.
	SinkAccessToken = "sink.access-token"
	// SinkConflict means that the notification destination changed since the caller read it.
	SinkConflict = "sink.conflict"
	// SinkInvalid means that the notification destination is not valid.
	SinkInvalid = "sink.invalid"
	// SinkNoSender means that the server cannot send to this kind of notification destination.
	SinkNoSender = "sink.no-sender"
	// SinkNoURL means that the notification destination has no URL that the server can use.
	SinkNoURL = "sink.no-url"
	// SinkNotFound means that the notification destination does not exist.
	SinkNotFound = "sink.not-found"
	// SinkPlatformHost means that the URL of the notification destination names a host of the platform.
	SinkPlatformHost = "sink.platform-host"
	// SinkTestRateLimited means that the request is over the limit of tests of the notification destination or of the
	// namespace.
	SinkTestRateLimited = "sink.test-rate-limited"
)

// sinkSpecs are the codes of the notification destinations.
func sinkSpecs() []Spec {
	return []Spec{
		{
			Code:    SinkAccessToken,
			Meaning: "the access token of the notification destination is not valid for its platform",
			Args:    nil,
		},
		{Code: SinkConflict, Meaning: "the notification destination changed since the caller read it", Args: nil},
		{Code: SinkInvalid, Meaning: "the notification destination is not valid", Args: nil},
		{Code: SinkNoSender, Meaning: "the server cannot send to this kind of notification destination", Args: nil},
		{Code: SinkNoURL, Meaning: "the notification destination has no URL that the server can use", Args: nil},
		{Code: SinkNotFound, Meaning: "the notification destination does not exist", Args: nil},
		{
			Code:    SinkPlatformHost,
			Meaning: "the URL of the notification destination names a host of the platform",
			Args:    nil,
		},
		{
			Code:    SinkTestRateLimited,
			Meaning: "the request is over the limit of tests of the notification destination or of the namespace",
			Args:    []Arg{{Name: "retryAfterSeconds", Kind: KindQuantity}},
		},
	}
}
