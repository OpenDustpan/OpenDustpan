package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// SinkAccessToken means that the access token of the notification destination is not valid for its platform.
	SinkAccessToken = "sink.access-token"
	// SinkConflict means that the notification destination changed since the caller read it.
	SinkConflict = "sink.conflict"
	// SinkDeliveryNotFound means that the delivery does not exist in the namespace.
	SinkDeliveryNotFound = "sink.delivery-not-found"
	// SinkDeliveryNotRedeliverable means that the delivery cannot be sent again now.
	SinkDeliveryNotRedeliverable = "sink.delivery-not-redeliverable"
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
	// SinkSubscriptionConflict means that the subscription changed since the caller read it.
	SinkSubscriptionConflict = "sink.subscription-conflict"
	// SinkSubscriptionInvalid means that the subscription is not valid.
	SinkSubscriptionInvalid = "sink.subscription-invalid"
	// SinkSubscriptionNotFound means that the subscription does not exist in the namespace.
	SinkSubscriptionNotFound = "sink.subscription-not-found"
	// SinkSubscriptionSinkNotFound means that the subscription names a notification destination that the namespace
	// does not hold.
	SinkSubscriptionSinkNotFound = "sink.subscription-sink-not-found"
	// SinkTestLimitUnavailable means that the server cannot count the tests of notification destinations, so it
	// refuses the test.
	SinkTestLimitUnavailable = "sink.test-limit-unavailable"
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
		{Code: SinkDeliveryNotFound, Meaning: "the delivery does not exist in the namespace", Args: nil},
		{Code: SinkDeliveryNotRedeliverable, Meaning: "the delivery cannot be sent again now", Args: nil},
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
			Code:    SinkSubscriptionConflict,
			Meaning: "the subscription changed since the caller read it",
			Args:    nil,
		},
		{Code: SinkSubscriptionInvalid, Meaning: "the subscription is not valid", Args: nil},
		{Code: SinkSubscriptionNotFound, Meaning: "the subscription does not exist in the namespace", Args: nil},
		{
			Code:    SinkSubscriptionSinkNotFound,
			Meaning: "the subscription names a notification destination that the namespace does not hold",
			Args:    nil,
		},
		{
			Code:    SinkTestLimitUnavailable,
			Meaning: "the server cannot count the tests of notification destinations, so it refuses the test",
			Args:    nil,
		},
		{
			Code:    SinkTestRateLimited,
			Meaning: "the request is over the limit of tests of the notification destination or of the namespace",
			Args:    []Arg{{Name: "retryAfterSeconds", Kind: KindQuantity}},
		},
	}
}
