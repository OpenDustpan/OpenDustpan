package sinks

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TakeForTest calls take on a new limiter. Tests in package sinks_test use it
// to send a name and a sink ID to the limiter without the service.
func TakeForTest(
	ctx context.Context, counter authgate.FailureCounter, ns namespace.Name, id string, now time.Time,
) (time.Duration, error) {
	return newTestLimiter(counter).take(ctx, ns, id, now)
}

// SinkKeyForTest returns the counter key of the sink limit of id in ns.
func SinkKeyForTest(ns namespace.Name, id string) string {
	return sinkKeyPrefix + ns.String() + "/" + id
}

// NamespaceKeyForTest returns the counter key of the namespace limit of ns.
func NamespaceKeyForTest(ns namespace.Name) string {
	return namespaceKeyPrefix + ns.String()
}
