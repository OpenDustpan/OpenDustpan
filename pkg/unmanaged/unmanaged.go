// Package unmanaged finds unmanaged resources: cloud resources that do not
// have exactly one unit that manages them. It has three kinds of finding:
//
//   - not in any unit: a scan of the cloud sees the resource, and the state
//     of no unit holds it;
//   - left behind: the resource is in the latest state of a deleted unit;
//   - managed twice: the latest states of two or more units hold the same
//     cloud resource, from one tool or from both.
//
// The server reads no cloud. A scan runs on a runner of the team, with
// read-only credentials, and uploads a scan report. The server matches the
// report against the resource index, a tool-neutral projection of the
// resources in the latest state of each unit, and classifies each resource.
// Left behind and managed twice need no scan: the resource index alone
// finds them.
//
// A finding is namespace data. Each write carries the fence of its namespace
// and appends its audit entry in the same transaction.
package unmanaged

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Sentinel errors of the package and its ports.
var (
	// ErrInvalidKey reports a resource key that fails validation.
	ErrInvalidKey = errors.New("invalid resource key")
	// ErrInvalidReport reports a scan report, or a line of one, that fails
	// validation.
	ErrInvalidReport = errors.New("invalid scan report")
	// ErrInvalidTarget reports a scan target that fails validation.
	ErrInvalidTarget = errors.New("invalid scan target")
	// ErrInvalidRule reports an ignore rule that fails validation.
	ErrInvalidRule = errors.New("invalid ignore rule")
	// ErrInvalidReason reports an ignore reason that is empty, too long or
	// holds control characters.
	ErrInvalidReason = errors.New("invalid reason")
	// ErrTransition reports an event that the state table does not allow
	// in the current state.
	ErrTransition = errors.New("transition not allowed")
	// ErrStatusChanged reports a finding whose status changed after the
	// caller read it. The caller reads it again.
	ErrStatusChanged = errors.New("the finding changed")
	// ErrActionKind reports an action that does not apply to the kind of
	// the finding, for example a choice of owner for a resource that is
	// not managed twice.
	ErrActionKind = errors.New("the action does not apply to this kind of finding")
	// ErrNotFound reports a finding, a scan, a scan target or an ignore rule
	// that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists reports a scan target or an ignore rule whose name is
	// taken.
	ErrAlreadyExists = errors.New("already exists")
	// ErrScanBusy reports the start of a scan for a target that has a scan
	// that did not end.
	ErrScanBusy = errors.New("a scan of this target did not end")
	// ErrLimit reports a write above a limit of the namespace.
	ErrLimit = errors.New("limit reached")
)

// The actions of unmanaged-resource detection. They are not yet in the
// action list of package identity. Each one names its object.
const (
	// ActionNamespaceReadUnmanaged allows reading the findings, the scans,
	// the scan targets and the ignore rules of a namespace. Its object is
	// the namespace. A finding names only the units that the caller can
	// read, and counts the others.
	ActionNamespaceReadUnmanaged identity.Action = "namespace:read-unmanaged"
	// ActionNamespaceTriageUnmanaged allows the actions on a finding:
	// ignore, stop ignoring, choose an owner, mark for delete, and start an
	// import or code generation. It also allows the ignore rules. Its
	// object is the namespace. An action that starts a job also needs the
	// action of the job on its unit.
	ActionNamespaceTriageUnmanaged identity.Action = "namespace:triage-unmanaged"
	// ActionNamespaceManageScans allows creating, changing and deleting the
	// scan targets of a namespace, with their schedule, their runner pool and
	// the command that their runner starts. Its object is the namespace.
	ActionNamespaceManageScans identity.Action = "namespace:manage-scans"
	// ActionNamespaceStartScan allows starting and canceling a scan of a
	// scan target now. Its object is the namespace.
	ActionNamespaceStartScan identity.Action = "namespace:start-scan"
)

// Kind is the kind of an unmanaged resource.
type Kind string

// The kinds of unmanaged resource.
const (
	KindNotInAnyUnit Kind = "not-in-any-unit"
	KindLeftBehind   Kind = "left-behind"
	KindManagedTwice Kind = "managed-twice"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return k == KindNotInAnyUnit || k == KindLeftBehind || k == KindManagedTwice
}

// Bounds of a resource key and of a report line.
const (
	// MaxKeyIDBytes bounds the cloud ID of a resource key.
	MaxKeyIDBytes = 2 << 10
	// maxProviderLength bounds the provider of a resource key.
	maxProviderLength = 50
	// arnParts is the number of parts of an AWS ARN, split at ':'.
	arnParts = 6
)

// The providers whose cloud IDs the server normalizes. Any other provider
// name that is a valid identifier is accepted, and its ID is compared as it
// is.
const (
	ProviderAWS        = "aws"
	ProviderAzure      = "azure"
	ProviderGCP        = "gcp"
	ProviderKubernetes = "kubernetes"
)

// ResourceKey is the tool-neutral identity of a cloud resource: the provider
// and the ID that the cloud gives the resource. For AWS it is the ARN, for
// Azure the resource ID, for Google Cloud the full resource name, and for
// Kubernetes "<cluster>/<API group>/<kind>/<namespace>/<name>". Two
// resource keys name the same cloud resource when they are equal after
// NormalizeKey.
type ResourceKey struct {
	Provider string
	ID       string
}

// String returns k as <provider>|<ID>.
func (k ResourceKey) String() string { return k.Provider + "|" + k.ID }

// NormalizeKey checks k and returns its normal form. It returns ErrInvalidKey
// for an invalid provider, an empty ID, an ID above MaxKeyIDBytes or with
// space or control characters, and an ID that does not have the form of its
// known provider.
//
// The rules: an Azure ID is in lowercase with no trailing slash, because
// Azure compares resource IDs with no case. Other IDs keep their case.
func NormalizeKey(k ResourceKey) (ResourceKey, error) {
	if !validProvider(k.Provider) {
		return k, fmt.Errorf("%w: provider %q", ErrInvalidKey, k.Provider)
	}

	if k.ID == "" || len(k.ID) > MaxKeyIDBytes || strings.ContainsFunc(k.ID, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return k, fmt.Errorf("%w: ID", ErrInvalidKey)
	}

	return normalizeID(k)
}

// normalizeID checks that the ID of k has the form of its known provider and
// returns k in its normal form. It returns ErrInvalidKey.
func normalizeID(k ResourceKey) (ResourceKey, error) {
	var err error

	switch k.Provider {
	case ProviderAWS:
		err = checkAWSID(k.ID)
	case ProviderAzure:
		k.ID = strings.TrimRight(strings.ToLower(k.ID), "/")
		err = checkAzureID(k.ID)
	case ProviderGCP:
		err = checkGCPID(k.ID)
	case ProviderKubernetes:
		err = checkKubernetesID(k.ID)
	default:
		// The ID of another provider stays as it is.
	}

	return k, err
}

// checkAWSID reports whether id is an ARN. It returns ErrInvalidKey.
func checkAWSID(id string) error {
	if parts := strings.SplitN(id, ":", arnParts); len(parts) < arnParts || parts[0] != "arn" || parts[2] == "" {
		return fmt.Errorf("%w: an AWS ID is an ARN", ErrInvalidKey)
	}

	return nil
}

// checkAzureID reports whether id, in its normal form, is an Azure resource
// ID. It returns ErrInvalidKey.
func checkAzureID(id string) error {
	if !strings.HasPrefix(id, "/subscriptions/") {
		return fmt.Errorf("%w: an Azure ID starts with /subscriptions/", ErrInvalidKey)
	}

	return nil
}

// checkGCPID reports whether id is a Google Cloud full resource name. It
// returns ErrInvalidKey.
func checkGCPID(id string) error {
	if !strings.HasPrefix(id, "//") || !strings.Contains(id[2:], "/") {
		return fmt.Errorf("%w: a Google Cloud ID is a full resource name", ErrInvalidKey)
	}

	return nil
}

// checkKubernetesID reports whether id has the form
// <cluster>/<group>/<kind>/<namespace>/<name>. It returns ErrInvalidKey.
func checkKubernetesID(id string) error {
	if parts := strings.Split(id, "/"); len(parts) != 5 || parts[0] == "" || parts[2] == "" || parts[4] == "" {
		return fmt.Errorf("%w: a Kubernetes ID is <cluster>/<group>/<kind>/<namespace>/<name>", ErrInvalidKey)
	}

	return nil
}

// Scope returns the account scope of a normalized key: the AWS account, the
// Azure subscription, the Google Cloud project or the Kubernetes cluster. It
// returns the empty string when the key has no scope that the server knows,
// for example an AWS ARN with no account, or another provider.
func Scope(k ResourceKey) string {
	switch k.Provider {
	case ProviderAWS:
		if parts := strings.SplitN(k.ID, ":", arnParts); len(parts) == arnParts {
			return parts[4]
		}
	case ProviderAzure:
		return segmentAfter(k.ID, "/subscriptions/")
	case ProviderGCP:
		return segmentAfter(k.ID, "/projects/")
	case ProviderKubernetes:
		first, _, _ := strings.Cut(k.ID, "/")

		return first
	default:
		// The server knows no scope of another provider.
	}

	return ""
}

// validProvider reports whether p is a valid provider name: a lowercase
// identifier.
func validProvider(p string) bool {
	return ident.Valid(p, maxProviderLength) && p == strings.ToLower(p)
}

func segmentAfter(s, marker string) string {
	_, rest, ok := strings.Cut(s, marker)
	if !ok {
		return ""
	}

	seg, _, _ := strings.Cut(rest, "/")

	return seg
}
