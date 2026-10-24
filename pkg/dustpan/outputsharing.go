package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// OutputSharingSettingsStore is a namespace store that also keeps the output
// sharing setting of each namespace. The SQLite and the PostgreSQL stores
// implement it. With such a store, the server serves the setting under
// /api/dustpan/v1/namespaces/{namespace}/output-sharing.
type OutputSharingSettingsStore interface {
	// OutputSharingSettings returns the port of the setting.
	OutputSharingSettings() outputshare.Settings
}

// newOutputSharingService builds the service of the output sharing setting on
// the namespace store when that store also keeps the setting. A custom store
// without it gets no service, and the server serves no setting route.
func newOutputSharingService(cfg *settings) (*outputsharesvc.Service, error) {
	source, ok := cfg.namespaceStore.(OutputSharingSettingsStore)
	if !ok {
		return nil, nil //nolint:nilnil // No setting service: the store keeps no setting.
	}

	svc, err := outputsharesvc.New(outputsharesvc.Config{
		Settings:   source.OutputSharingSettings(),
		Namespaces: cfg.namespaceStore,
		Authorizer: cfg.authorizer,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("output sharing service: %w", err)
	}

	return svc, nil
}

// newOutputStates returns the States port over the stores of both tools.
func newOutputStates(svc services) outputsharesvc.ToolStates {
	return outputsharesvc.ToolStates{
		opentofuTool: workspace.NewOutputStates(svc.workspaces),
		pulumiTool:   stack.NewOutputStates(svc.stacks, svc.updates),
	}
}

// newOutputChecker builds the checker of the read of outputs on the services
// of both tools. Each adapter that answers outputs asks it.
func newOutputChecker(cfg *settings, svc services) (*outputsharesvc.Checker, error) {
	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	checker, err := outputsharesvc.NewChecker(outputsharesvc.CheckerConfig{
		Authorizer: cfg.authorizer,
		States:     newOutputStates(svc),
		Namespaces: svc.namespaces,
		Audit:      entries,
		Logger:     cfg.logger,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("output checker: %w", err)
	}

	return checker, nil
}

// newOutputReader builds the reader of the root outputs of a unit, for the
// tool-neutral outputs route.
func newOutputReader(cfg *settings, svc services) (*outputsharesvc.Reader, error) {
	checker, err := newOutputChecker(cfg, svc)
	if err != nil {
		return nil, err
	}

	reader, err := outputsharesvc.NewReader(outputsharesvc.ReaderConfig{
		States:  newOutputStates(svc),
		Checker: checker,
		Sources: map[string]outputshare.Outputs{
			opentofuTool: workspace.NewOutputStates(svc.workspaces),
			pulumiTool:   stack.NewOutputStates(svc.stacks, svc.updates),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("output reader: %w", err)
	}

	return reader, nil
}

// newOutputSharingParts builds the service of the output sharing setting and
// the reader of outputs for the inventory API.
func newOutputSharingParts(cfg *settings, svc services) (*outputsharesvc.Service, *outputsharesvc.Reader, error) {
	sharing, err := newOutputSharingService(cfg)
	if err != nil {
		return nil, nil, err
	}

	outputs, err := newOutputReader(cfg, svc)
	if err != nil {
		return nil, nil, err
	}

	return sharing, outputs, nil
}
