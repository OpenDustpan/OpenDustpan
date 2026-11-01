package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
)

// devRefusalError is the error of a refused dev backup. It wraps
// ErrDevBackupRefused and carries the text that the restore row and the API
// answer show, with the words of AllowDevHint.
type devRefusalError struct {
	text string
}

// Error implements error.
func (e *devRefusalError) Error() string {
	return ErrDevBackupRefused.Error()
}

// Is makes errors.Is(err, ErrDevBackupRefused) true.
func (e *devRefusalError) Is(target error) bool {
	return target == ErrDevBackupRefused
}

// Text returns the refusal text for an operator.
func (e *devRefusalError) Text() string {
	return e.text
}

// refusalText returns the text of a refusal. reason names why the backup is
// refused: the backup claims to come from a dev installation, or the staged
// data holds dev people. hint names how to allow the restore, and is left out
// when empty.
func refusalSentence(reason, hint string) string {
	text := "This installation is a production installation. " + reason + " We do not recommend this restore."
	if hint != "" {
		text += " To restore it, start the server with " + hint + "."
	}

	return text
}

// The reasons of a refusal.
const (
	reasonDevBackup = "The backup is from a dev installation."
	reasonDevData   = "The backup holds people of a dev installation."
	// reasonDevSwap fits the refusal of the swap, which does not know why the
	// staged data holds dev people: also a backup that claimed production.
	reasonDevSwap = "The backup holds people of a dev installation, or it is from a dev installation."
)

// warningTexts holds the text of each warning that a restore answer carries.
//
//nolint:gochecknoglobals // A frozen lookup table of fixed sentences.
var warningTexts = map[RestoreWarningCode]string{
	WarnDevBackupIntoProduction: "This restore removed the dev users and their grants. " +
		"Things that depend on them may break. We do not recommend this restore.",
	WarnDevBackupWouldRemove: "This restore would remove the dev users and their grants. " +
		"This check changed no data. Things that depend on them may break. We do not recommend this restore.",
	WarnProductionBackupIntoDev: "This backup is from a production installation. " +
		"Its tokens and sessions are now in a dev data directory.",
	WarnAuditAppendFailed: "The restore finished. The server could not write an audit entry for the removed dev data. " +
		"The restore record holds the report.",
	WarnDamagedLiveBlobs: "The backup before this restore found live blobs that were missing or did not match " +
		"their records. The restore wrote the checked bytes of the backup there. The audit log names each blob.",
}

// WarningFor returns the warning of code, and false for an unknown code.
func WarningFor(code RestoreWarningCode) (RestoreWarning, bool) {
	text, ok := warningTexts[code]
	if !ok {
		return RestoreWarning{Code: code, Text: ""}, false
	}

	return RestoreWarning{Code: code, Text: text}, true
}

// Warnings returns the warnings of the record, in the order of the codes of
// its report. A record without a report has none.
func (r RestoreRecord) Warnings() []RestoreWarning {
	if r.DevReport == nil {
		return nil
	}

	out := make([]RestoreWarning, 0, len(r.DevReport.Warnings))

	for _, code := range r.DevReport.Warnings {
		if warning, ok := WarningFor(code); ok {
			out = append(out, warning)
		}
	}

	return out
}

// devRun holds what the dev data rules of one restore know: the kind of the
// live installation, the kind that the pinned manifest claims, and the report
// of the staged data.
type devRun struct {
	live   installation.Kind
	backup installation.Kind
	report DevStripReport
	// checkOnly is true for a check-only restore, which removes nothing.
	checkOnly bool
}

// production reports whether the live installation is a production
// installation, the only side that the dev data rules protect.
func (d *devRun) production() bool {
	return d.live == installation.KindProduction
}

// warnings returns the codes of the warnings that the restore carries.
func (d *devRun) warnings() []RestoreWarningCode {
	var codes []RestoreWarningCode

	switch {
	case d.production() && !d.report.Empty() && d.checkOnly:
		codes = append(codes, WarnDevBackupWouldRemove)
	case d.production() && !d.report.Empty():
		codes = append(codes, WarnDevBackupIntoProduction)
	case d.live == installation.KindDev && d.backup == installation.KindProduction:
		codes = append(codes, WarnProductionBackupIntoDev)
	default:
	}

	return codes
}

// devKinds reads the live installation kind and compares it with the kind of
// the pinned manifest, before the restore enters maintenance mode, takes the
// backup of the live data or stages anything. A live kind that cannot be read
// fails the restore: the rules never skip because a port failed.
func (s *RestoreService) devKinds(ctx context.Context, pinned pinnedManifest) (*devRun, error) {
	live, err := s.live.ReadKind(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the installation kind of the live data: %w", err)
	}

	if !live.Valid() {
		return nil, fmt.Errorf("read the installation kind of the live data: %w", installation.ErrUnavailable)
	}

	run := &devRun{live: live, backup: pinned.kind, report: emptyDevReport(), checkOnly: false}

	if live == installation.KindProduction && pinned.kind == installation.KindDev && !s.allowDev {
		return nil, &devRefusalError{text: refusalSentence(reasonDevBackup, s.devHint)}
	}

	return run, nil
}

// emptyDevReport returns a report with no removal.
func emptyDevReport() DevStripReport {
	return DevStripReport{
		Principals:      nil,
		OtherPrincipals: 0,
		OtherCounts:     nil,
		Classes:         nil,
		Approvers:       nil,
		Warnings:        nil,
	}
}

// logRemoval writes one warning to the log for each class that the strip
// removed rows of, with the count. A line holds no subject, no token and no
// session ID.
func (s *RestoreService) logRemoval(ctx context.Context, report DevStripReport) {
	classes := make([]string, 0, len(report.Classes))

	for class, n := range report.Classes {
		if n > 0 {
			classes = append(classes, string(class))
		}
	}

	slices.Sort(classes)

	for _, class := range classes {
		s.logger.WarnContext(ctx, "the restore removed dev data from the staged data",
			slog.String("class", class), slog.Int64("count", report.Classes[DevRemovalClass(class)]))
	}
}

// serverAuditAppender is the port of the server audit log that the restore
// service writes to after the swap.
type serverAuditAppender interface {
	AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
}

// devAuditObjects returns the object text of each audit entry of a report.
// The subjects are cleaned a second time here: the adapters clean them, and
// this layer does not rely on that. No line holds a token, a session ID or a
// digest.
func devAuditObjects(id RestoreID, backupID ID, report DevStripReport) []string {
	prefix := "restore " + id.String() + " of backup " + backupID.String() + " removed "

	var out []string

	for _, removal := range report.Principals {
		subject, cut := CleanSubject(removal.Subject)
		cut = cut || removal.SubjectCut

		line := prefix + "dev principal " + strconv.Quote(subject)
		if cut {
			line += " (subject cut)"
		}

		out = append(out, line+countsText(removal.Counts))
	}

	if report.OtherPrincipals > 0 {
		out = append(out, prefix+strconv.FormatInt(report.OtherPrincipals, 10)+
			" other dev principals"+countsText(report.OtherCounts))
	}

	for _, class := range []DevRemovalClass{
		RemovedProviders, RemovedSignInRequests, RemovedApprovers, RemovedDangling,
	} {
		if n := report.Classes[class]; n > 0 {
			out = append(out, prefix+strconv.FormatInt(n, 10)+" "+string(class)+approverNames(class, report))
		}
	}

	return out
}

// approverNames lists the objects that lost an approver, for the entry of the
// class approvers.
func approverNames(class DevRemovalClass, report DevStripReport) string {
	if class != RemovedApprovers || len(report.Approvers) == 0 {
		return ""
	}

	names := make([]string, 0, len(report.Approvers))

	for _, a := range report.Approvers {
		name := a.Kind + " " + strconv.Quote(a.Scope)
		if a.ID != "" {
			name += " " + strconv.Quote(a.ID)
		}

		if a.Replaced {
			name += " (no approver left)"
		}

		names = append(names, name)
	}

	return ": " + strings.Join(names, ", ")
}

// countsText renders the counts of a removal in the order of the class names.
func countsText(counts map[DevRemovalClass]int64) string {
	if len(counts) == 0 {
		return ""
	}

	classes := make([]string, 0, len(counts))

	for class, n := range counts {
		if n > 0 {
			classes = append(classes, string(class)+"="+strconv.FormatInt(n, 10))
		}
	}

	slices.Sort(classes)

	if len(classes) == 0 {
		return ""
	}

	return " (" + strings.Join(classes, " ") + ")"
}

// RefusalText returns the text of a refused dev backup for an operator, with
// the words of AllowDevHint, and false when err is no such refusal.
func RefusalText(err error) (string, bool) {
	if refusal, ok := errors.AsType[*devRefusalError](err); ok {
		return refusal.Text(), true
	}

	return "", false
}

// startDevCheck is the check of Start: it reads the manifest of the backup
// and the live kind, and refuses a dev backup into a production installation
// at once, before the restore row exists, so the API answers 409 and no
// restore starts. A manifest that cannot be read is no refusal here: the
// restore reads it again and fails with its own error. The check runs again
// in the restore, on the pinned manifest. It returns the warnings that the
// kinds already decide.
func (s *RestoreService) startDevCheck(ctx context.Context, id ID) ([]RestoreWarningCode, error) {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return nil, nil //nolint:nilerr // The restore reads the manifest again and reports its error.
	}

	run, err := s.devKinds(
		ctx,
		pinnedManifest{
			sqlSize:  0,
			sqlSHA:   "",
			schema:   0,
			dialect:  "",
			kind:     manifest.InstallationKind,
			wrappers: nil,
			digest:   "",
		},
	)
	if err != nil {
		return nil, err
	}

	switch {
	case run.production() && run.backup == installation.KindDev:
		return []RestoreWarningCode{WarnDevBackupIntoProduction}, nil
	case run.live == installation.KindDev && run.backup == installation.KindProduction:
		return []RestoreWarningCode{WarnProductionBackupIntoDev}, nil
	default:
		return nil, nil
	}
}
