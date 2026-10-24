package sqlite //nolint:testpackage // The white-box template state is unexported, so the exporter must stay in that package.

import (
	"context"
	"fmt"
	"os"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// RemoveInternalTemplates removes the white-box template directory built for
// this test binary. It skips removal when the template was never built.
func RemoveInternalTemplates() error {
	if internalTemplateState.dir == "" {
		return nil
	}

	if err := os.RemoveAll(internalTemplateState.dir); err != nil {
		return fmt.Errorf("remove store template: %w", err)
	}

	return nil
}

// CheckSettingFence runs the comparison of the fence and the namespace that
// SetSetting makes before it opens a transaction.
func CheckSettingFence(ns namespace.Name, fence namespace.Fence) error {
	return settingFenceBinds(ns, fence)
}

// SettingWrite is the input of WriteSettingInTx.
type SettingWrite struct {
	Namespace namespace.Name
	Value     outputshare.Default
	Fence     namespace.Fence
	Entry     audit.Entry
}

// WriteSettingInTx writes the setting through the transaction of SetSetting
// and skips the pre-check, so a test reaches the layer inside the
// transaction alone.
func WriteSettingInTx(ctx context.Context, s *Store, w SettingWrite) error {
	return outputSharingSettings{s: s}.writeSetting(ctx, w.Namespace, w.Value, w.Fence, w.Entry)
}

// SetBetweenPageReads sets the hook that QueryStacks and QueryUnits run after
// the statement of the page and before the statement of its tags or labels.
func SetBetweenPageReads(s *Store, hook func(ctx context.Context)) {
	s.betweenPageReads = hook
}
