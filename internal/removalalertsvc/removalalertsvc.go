// Package removalalertsvc alerts the administrators when one source removes
// members from groups at a rate that reaches the limits of the removal
// alert: a count of members that one source removed in a rolling window, or
// a percent of the members of one group that it removed in that window,
// whichever comes first. The server counts members, not requests. One source
// gets one alert in each window, for all its groups, or a repeat when a write
// stops after the notification or two writes race. A marker time after the
// time of the removal counts as no marker and writes an audit entry. An alert
// that fails to write leaves no marker, so the next removal of the source
// tries the alert again. The limits are installation settings, and the service
// reads them at each record. A reader that fails gives the default limits,
// never no limit.
package removalalertsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("removal alert service needs a dependency")

// The values of the attribute limit of an alert.
const (
	limitCount   = "count"
	limitPercent = "percent"
)

// The keys of the source of an alert in an attribute and in an audit change.
const (
	keySourceKind = "source.kind"
	keySourceID   = "source.id"
)

// MemberCounter counts the members of one group. The principal store
// implements it with ListMembers: the service reads the total of the
// answer.
type MemberCounter interface {
	// ListMembers is principal.Store.ListMembers. The service reads the
	// total of the answer and ignores the page.
	ListMembers(ctx context.Context, groupID string, q principal.Query) ([]principal.Member, int64, error)
}

// Limits reads the limits of the removal alert. A reader that fails, or
// that answers a value outside the bounds, gives the default limits.
type Limits func(ctx context.Context) (principal.RemovalAlertLimits, error)

// Config holds the ports of a Service.
type Config struct {
	// Log stores each removal and answers the totals of its source.
	Log principal.RemovalLog
	// Members counts the members of a group.
	Members MemberCounter
	// Outbox receives the alert notification of the server scope.
	Outbox notify.Outbox
	// Audit appends the alert entry in the server scope.
	Audit audit.ServerStore
	// Logger writes the WARN line of the alert.
	Logger *slog.Logger
	// Now is the clock. The service reads it once for each removal.
	Now func() time.Time
	// Limits reads the removal alert limits. Nil, a reader that fails and
	// a value outside the bounds give the default limits.
	Limits Limits
}

// Service alerts the administrators when one source removes members from
// groups at a rate that crosses the removal alert limits.
type Service struct {
	cfg Config
}

// New returns a Service. It returns ErrMissingDependency when cfg lacks a
// port, a logger or a clock.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Log == nil:
		return nil, fmt.Errorf("%w: removal log", ErrMissingDependency)
	case cfg.Members == nil:
		return nil, fmt.Errorf("%w: member counter", ErrMissingDependency)
	case cfg.Outbox == nil:
		return nil, fmt.Errorf("%w: outbox", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit log", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.Now == nil:
		return nil, fmt.Errorf("%w: clock", ErrMissingDependency)
	default:
		return &Service{cfg: cfg}, nil
	}
}

// Record stores one removal of one member from each group in groupIDs for
// source, and alerts the administrators when the totals of the source reach
// a limit and the source got no alert in the window. The caller calls it
// after the removals were committed. It dedupes the group IDs.
//
// It fails closed: an error of the count read, of the removal log, of the
// outbox, of the audit log or of the alert marker makes it return an error
// that wraps the error, so the caller can record the failed alert. An alert
// that fails to write leaves no marker, so the next removal of the source
// tries the alert again.
func (s *Service) Record(ctx context.Context, source principal.RemovalSource, groupIDs []string) error {
	for _, groupID := range dedupe(groupIDs) {
		if err := s.recordOne(ctx, source, groupID); err != nil {
			return err
		}
	}

	return nil
}

// recordOne stores one removal of one member from groupID and alerts the
// administrators when its totals reach a limit and the source got no alert
// in the window.
func (s *Service) recordOne(ctx context.Context, source principal.RemovalSource, groupID string) error {
	source.ID = alertText(source.ID)
	groupID = alertText(groupID)

	limits := s.limits(ctx)
	at := s.cfg.Now()

	_, total, err := s.cfg.Members.ListMembers(ctx, groupID, principal.Query{After: "", Low: "", High: "", Limit: 1})
	if err != nil {
		return fmt.Errorf("count the members of group %q: %w", groupID, err)
	}

	totals, err := s.cfg.Log.AddRemoval(ctx, principal.Removal{
		Source: source, GroupID: groupID, Removed: 1, MembersBefore: int(total) + 1, At: at,
	}, limits.Window)
	if err != nil {
		return fmt.Errorf("store the removal of group %q: %w", groupID, err)
	}

	limit, percent, reached := atOrAbove(totals, limits)
	if !reached {
		return nil
	}

	if !totals.AlertedAt.IsZero() {
		switch {
		case totals.AlertedAt.After(at):
			if err := s.invalidMarker(ctx, source, groupID, totals.AlertedAt, at); err != nil {
				return err
			}
		case totals.AlertedAt.After(at.Add(-limits.Window)):
			return nil
		default:
		}
	}

	return s.alert(ctx, alertInput{
		source: source, groupID: groupID, totals: totals, percent: percent, limit: limit,
		limits: limits, at: at,
	})
}

// invalidMarker writes the WARN line and the server audit entry of an alert
// marker whose time is after the time of the removal. It fails closed: an
// error of the audit log returns an error that wraps the error, and the alert
// is not written.
func (s *Service) invalidMarker(
	ctx context.Context, source principal.RemovalSource, groupID string, marker, at time.Time,
) error {
	markerText := marker.UTC().Format(time.RFC3339Nano)
	atText := at.UTC().Format(time.RFC3339Nano)

	s.cfg.Logger.LogAttrs(ctx, slog.LevelWarn,
		"removal alert: the alert marker of one source is after the time of the removal",
		slog.String("code", errcode.PrincipalRemovalAlertMarkerInvalid),
		slog.String("source_kind", string(source.Kind)), slog.String("source_id", source.ID),
		slog.String("alerted_at", markerText), slog.String("at", atText))

	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: string(source.Kind) + ":" + source.ID,
		IDPSubject: "", TokenID: "", Action: principal.AuditActionMassRemovalAlert,
		Object: principal.GroupRef(groupID), Version: "", ValuePath: "", ValueCount: nil,
		Changes: []audit.Change{
			{Key: keySourceKind, Old: nil, New: string(source.Kind), Layer: "", TakesEffect: ""},
			{Key: keySourceID, Old: nil, New: source.ID, Layer: "", TakesEffect: ""},
			{Key: "alerted_at", Old: nil, New: markerText, Layer: "", TakesEffect: ""},
			{Key: "at", Old: nil, New: atText, Layer: "", TakesEffect: ""},
		},
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindSecurity, Outcome: audit.OutcomeFailure, RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Reason: errcode.PrincipalRemovalAlertMarkerInvalid,
	}

	if _, err := s.cfg.Audit.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append the invalid alert marker entry of group %q: %w", groupID, err)
	}

	return nil
}

// alertInput holds the fields of one alert.
type alertInput struct {
	source  principal.RemovalSource
	groupID string
	totals  principal.RemovalTotals
	limits  principal.RemovalAlertLimits
	percent int
	limit   string
	at      time.Time
}

// alert writes the notification, the audit entry, the alert marker and the
// log line of one alert, in that order. It fails closed: an error of the
// outbox, of the audit log or of the marker makes it return an error that
// wraps the error, and the log line is not written. The next removal of the
// source then tries the alert again.
func (s *Service) alert(ctx context.Context, in alertInput) error {
	source, groupID, totals, limits := in.source, in.groupID, in.totals, in.limits

	// The keys of a notification attribute are lowercase letters, digits,
	// dots and dashes, so the source and the group use a dotted name.
	attrs := map[string]string{
		keySourceKind: string(source.Kind),
		keySourceID:   source.ID,
		"group.id":    groupID,
		"removed":     strconv.Itoa(totals.SourceRemoved),
		"percent":     strconv.Itoa(in.percent),
		"window":      limits.Window.String(),
		"limit":       in.limit,
	}

	summary := removalAlertSummary(source, groupID, totals.SourceRemoved, in.percent, limits.Window)

	note := notify.Notification{
		ID: ident.NewID(notify.IDPrefix), Type: notify.MembershipMassRemoval, Scope: audit.ServerScope,
		Object: notify.NotificationsObject(audit.ServerScope), ObjectID: "",
		Subject: history.ServerSubject, Agent: "", Severity: notify.SeverityWarning, Summary: summary,
		Links: nil, Attributes: attrs,
		Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
		DedupeKey:  "", At: in.at, Sequence: 0,
	}

	// The server scope has the zero fence: it is not a namespace.
	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

	if err := s.cfg.Outbox.Append(ctx, fence, []notify.Notification{note.Fit()}); err != nil {
		return fmt.Errorf("append the mass removal alert of group %q: %w", groupID, err)
	}

	entry := audit.Entry{
		At: in.at, Namespace: audit.ServerScope, Subject: string(source.Kind) + ":" + source.ID,
		IDPSubject: "", TokenID: "", Action: principal.AuditActionMassRemovalAlert,
		Object: principal.GroupRef(groupID), Version: "", ValuePath: "", ValueCount: nil,
		Changes: alertChanges(attrs), SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}

	if _, err := s.cfg.Audit.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append the mass removal alert entry of group %q: %w", groupID, err)
	}

	if err := s.cfg.Log.MarkRemovalAlert(ctx, source, in.at); err != nil {
		return fmt.Errorf("mark the mass removal alert of group %q: %w", groupID, err)
	}

	s.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "removal alert: the removals of one source reached a limit",
		slog.String("source_kind", string(source.Kind)), slog.String("source_id", source.ID),
		slog.String("group_id", groupID), slog.Int("removed", totals.SourceRemoved),
		slog.Int("percent", in.percent), slog.String("window", limits.Window.String()),
		slog.String("limit", in.limit))

	return nil
}

// limits returns the removal alert limits of the settings. A reader that is
// nil or that fails, and a value outside the bounds, give the default
// limits, never no limit.
func (s *Service) limits(ctx context.Context) principal.RemovalAlertLimits {
	if s.cfg.Limits == nil {
		return principal.DefaultRemovalAlertLimits()
	}

	l, err := s.cfg.Limits(ctx)
	if err != nil || !validLimits(l) {
		return principal.DefaultRemovalAlertLimits()
	}

	return l
}

// validLimits reports whether l holds a value inside the bounds of each
// removal alert setting.
func validLimits(l principal.RemovalAlertLimits) bool {
	return l.Count >= principal.MinRemovalAlertCount && l.Count <= principal.MaxRemovalAlertCount &&
		l.Percent >= principal.MinRemovalAlertPercent && l.Percent <= principal.MaxRemovalAlertPercent &&
		l.Window >= principal.MinRemovalAlertWindow && l.Window <= principal.MaxRemovalAlertWindow
}

// atOrAbove returns the limit that the totals reached, the percent of the
// members of the group that the source removed in the window, and true. It
// returns false when no limit is at or above. The count wins when both
// limits are at or above.
func atOrAbove(totals principal.RemovalTotals, limits principal.RemovalAlertLimits) (string, int, bool) {
	percent := percentOf(totals)

	if totals.SourceRemoved >= limits.Count {
		return limitCount, percent, true
	}

	if totals.GroupRemoved*100 >= limits.Percent*totals.GroupMembers {
		return limitPercent, percent, true
	}

	return "", percent, false
}

// percentOf returns the percent of the members of the group that the source
// removed in the window, rounded down. A group of no members gives 0.
func percentOf(totals principal.RemovalTotals) int {
	if totals.GroupMembers <= 0 {
		return 0
	}

	return totals.GroupRemoved * 100 / totals.GroupMembers
}

// removalAlertSummary returns the summary of an alert: the source kind and
// ID, the count, the percent and the window.
func removalAlertSummary(
	source principal.RemovalSource, groupID string, removed, percent int, window time.Duration,
) string {
	return fmt.Sprintf("The %s %s removed %d members in %s: %d percent of the members of group %s.",
		source.Kind, source.ID, removed, window, percent, groupID)
}

// alertChanges returns the audit changes of an alert, one for each
// attribute of its notification.
func alertChanges(attrs map[string]string) []audit.Change {
	keys := []string{keySourceKind, keySourceID, "group.id", "removed", "percent", "window", "limit"}
	out := make([]audit.Change, 0, len(keys))

	for _, key := range keys {
		out = append(out, audit.Change{Key: key, Old: nil, New: attrs[key], Layer: "", TakesEffect: ""})
	}

	return out
}

// alertText returns s without a CR or an LF, for a log line, a summary or
// an attribute. Defense in depth: Removal.Validate refuses a control
// character in the source ID and the group ID, and this removal stays so
// that the text of an alert can never hold a line end.
func alertText(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// dedupe returns the IDs of ids, each once, in order.
func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))

	for _, id := range ids {
		if seen[id] {
			continue
		}

		seen[id] = true
		out = append(out, id)
	}

	return out
}
