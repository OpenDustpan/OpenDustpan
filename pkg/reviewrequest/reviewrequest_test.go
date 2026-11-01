package reviewrequest_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/reviewrequest"
)

func TestModeValid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		mode reviewrequest.Mode
		want bool
	}{
		{reviewrequest.ModeMergedApprovals, true},
		{reviewrequest.ModeOpenDustpanMerges, true},
		{reviewrequest.ModeNoReviewRequests, true},
		{reviewrequest.ModeNoChangeOrders, true},
		{"", false},
		{"Merged-Approvals", false},
		{"merged-approvals ", false},
	} {
		if got := tc.mode.Valid(); got != tc.want {
			t.Errorf("Mode(%q).Valid() = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestStrictest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		modes []reviewrequest.Mode
		want  reviewrequest.Mode
	}{
		{"one mode", []reviewrequest.Mode{reviewrequest.ModeNoChangeOrders}, reviewrequest.ModeNoChangeOrders},
		{"mode 3 wins over each other mode", []reviewrequest.Mode{
			reviewrequest.ModeNoChangeOrders, reviewrequest.ModeNoReviewRequests,
			reviewrequest.ModeMergedApprovals, reviewrequest.ModeOpenDustpanMerges,
		}, reviewrequest.ModeNoReviewRequests},
		{"mode 2 wins over mode 1", []reviewrequest.Mode{
			reviewrequest.ModeMergedApprovals, reviewrequest.ModeOpenDustpanMerges,
		}, reviewrequest.ModeOpenDustpanMerges},
		{"mode 1 wins over mode 4", []reviewrequest.Mode{
			reviewrequest.ModeNoChangeOrders, reviewrequest.ModeMergedApprovals,
		}, reviewrequest.ModeMergedApprovals},
	} {
		got, err := reviewrequest.Strictest(tc.modes)
		if err != nil || got != tc.want {
			t.Errorf("%s: Strictest() = %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestStrictestRefusesInvalidInput(t *testing.T) {
	t.Parallel()

	for _, modes := range [][]reviewrequest.Mode{
		nil,
		{},
		{reviewrequest.ModeMergedApprovals, "unknown"},
		{""},
	} {
		if _, err := reviewrequest.Strictest(modes); !errors.Is(err, reviewrequest.ErrInvalid) {
			t.Errorf("Strictest(%q) error = %v, want ErrInvalid", modes, err)
		}
	}
}

func TestBindingLevelValid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		level reviewrequest.BindingLevel
		want  bool
	}{
		{reviewrequest.Level0, true},
		{reviewrequest.Level1, true},
		{reviewrequest.Level2, true},
		{"L3", false},
		{"", false},
		{"l1", false},
	} {
		if got := tc.level.Valid(); got != tc.want {
			t.Errorf("BindingLevel(%q).Valid() = %v, want %v", tc.level, got, tc.want)
		}
	}

	if reviewrequest.DefaultBindingLevel != reviewrequest.Level1 {
		t.Errorf("DefaultBindingLevel = %q, want L1", reviewrequest.DefaultBindingLevel)
	}
}

func TestOperationKindFloor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind  reviewrequest.OperationKind
		valid bool
		floor bool
	}{
		{reviewrequest.OperationCreate, true, false},
		{reviewrequest.OperationUpdate, true, false},
		{reviewrequest.OperationDelete, true, true},
		{reviewrequest.OperationReplaceCreateFirst, true, true},
		{reviewrequest.OperationReplaceDeleteFirst, true, true},
		{"replace", false, false},
		{"", false, false},
	} {
		if got := tc.kind.Valid(); got != tc.valid {
			t.Errorf("OperationKind(%q).Valid() = %v, want %v", tc.kind, got, tc.valid)
		}

		if got := tc.kind.Floor(); got != tc.floor {
			t.Errorf("OperationKind(%q).Floor() = %v, want %v", tc.kind, got, tc.floor)
		}
	}
}
