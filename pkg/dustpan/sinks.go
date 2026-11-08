package dustpan

import (
	"fmt"
	"net/http"
	"slices"

	notificationsapi "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/matrix"
	"opendustpan.org/opendustpan/pkg/notify/chat/mattermost"
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
	"opendustpan.org/opendustpan/pkg/notify/chat/teams"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/notify/webhook"
)

// sinkSettings holds the options of the notification sinks.
type sinkSettings struct {
	store   notify.SinkStore
	senders []notify.Sender
	ports   egress.Ports
}

// WithSinkStore sets the store of notification sinks. With it, and with a
// namespace store that keeps audit entries, the server serves the sink routes
// under /api/dustpan/v1/namespaces/{namespace}/sinks, the subscription and
// delivery routes beside them, and the test route of a
// sink sends one fixed message through the sender of its kind. The server
// seals the secret items of a sink with the data key of the namespace. Without
// the option, the server serves no sink route.
func WithSinkStore(st notify.SinkStore) Option {
	return func(o *settings) { o.sinks.store = st }
}

// WithSinkSenders adds senders for the test route of a sink, one for each sink
// kind. A sender replaces the built-in sender of its kind. The server has the
// signed webhook sender and the chat sender for Slack, Mattermost, Teams and Matrix
// built in, so a caller needs this option for a test double of one of them, or for a kind that has
// no built-in sender yet.
func WithSinkSenders(senders ...notify.Sender) Option {
	return func(o *settings) { o.sinks.senders = append(o.sinks.senders, senders...) }
}

// WithSinkPorts sets the ports that a notification sink may send to, as an
// egress.Ports value. Without the option, a sink sends to the port 443 only.
// The server checks the port when a person saves a sink, before each test,
// and in the built-in webhook sender before each send.
func WithSinkPorts(p egress.Ports) Option {
	return func(o *settings) { o.sinks.ports = p }
}

// sinkSenders returns the senders of the test route: the built-in signed
// webhook sender and the built-in chat sender (Slack, Mattermost, Teams and Matrix),
// unless a sender of the kind is set, and then the set ones.
func sinkSenders(cfg *settings) []notify.Sender {
	out := slices.Clone(cfg.sinks.senders)

	if !slices.ContainsFunc(out, func(s notify.Sender) bool { return s.Kind() == notify.SinkWebhook }) {
		out = append(out, webhook.New(webhook.WithPorts(cfg.sinks.ports)))
	}

	if !slices.ContainsFunc(out, func(s notify.Sender) bool { return s.Kind() == notify.SinkChat }) {
		out = append(
			out,
			chat.NewSender(
				[]chat.Renderer{slack.New(), mattermost.New(), teams.New(), matrix.New()},
				chat.WithPorts(cfg.sinks.ports),
			),
		)
	}

	return out
}

// newSinksHandler builds the sink API, or returns nil when no sink store is
// set.
func newSinksHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	if cfg.sinks.store == nil {
		return nil, nil //nolint:nilnil // No sink API: no sink store is set.
	}

	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	// The limits of the test route count in the database that the instances
	// share. A store that keeps no counter cannot hold them across instances,
	// so the server refuses to build the route.
	counter, ok := cfg.namespaceStore.(authgate.FailureCounter)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no shared count for the limits of tests: %w", ErrMissingOption)
	}

	service, err := sinks.NewService(sinks.Config{
		Counter:    counter,
		Store:      cfg.sinks.store,
		Sealer:     notify.SinkSealer{Namespaces: svc.secrets, Installation: nil},
		Namespaces: svc.namespaces,
		Senders:    sinkSenders(cfg),
		Ports:      cfg.sinks.ports,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("sink service: %w", err)
	}

	// The store of sinks is the store of notifications, which also keeps the
	// subscriptions and the deliveries. A store that keeps only sinks cannot
	// serve the subscription routes, so the server refuses to build them.
	subscriptions, ok := cfg.sinks.store.(notify.Subscriptions)
	if !ok {
		return nil, fmt.Errorf("sink store keeps no subscriptions: %w", ErrMissingOption)
	}

	deliveries, ok := cfg.sinks.store.(notify.Deliveries)
	if !ok {
		return nil, fmt.Errorf("sink store keeps no deliveries: %w", ErrMissingOption)
	}

	subscriptionService, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: subscriptions, Deliveries: deliveries, Namespaces: svc.namespaces, Audit: entries,
		Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("subscription service: %w", err)
	}

	idem, err := idempotencyMiddleware(cfg)
	if err != nil {
		return nil, err
	}

	handler, err := notificationsapi.NewHandler(notificationsapi.Config{
		Sinks:          service,
		Subscriptions:  subscriptionService,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          entries,
		Namespaces:     svc.namespaces,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Idempotency:    idem,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("sink api: %w", err)
	}

	return handler, nil
}
