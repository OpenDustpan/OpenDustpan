package outputsharetest_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
)

// memNamespace is one namespace of memSettings: its failover version, its
// stored setting and the actions of its audit entries.
type memNamespace struct {
	setting string
	audited []string
	version int64
}

// memSettings is an outputshare.Settings in memory. It proves that the cases
// of the setting hold for a correct adapter.
type memSettings struct {
	spaces map[namespace.Name]*memNamespace
	mu     sync.Mutex
}

var _ outputshare.Settings = (*memSettings)(nil)

func newMemSettings() *memSettings {
	return &memSettings{spaces: map[namespace.Name]*memNamespace{}, mu: sync.Mutex{}}
}

// Setting implements outputshare.Settings.
func (m *memSettings) Setting(_ context.Context, ns namespace.Name) (outputshare.Default, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return "", outputshare.ErrInvalid
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	space, ok := m.spaces[ns]
	if !ok {
		return "", namespace.ErrNotFound
	}

	d := outputshare.Default(space.setting)
	if err := d.Validate(); err != nil {
		return "", fmt.Errorf("stored setting: %w", err)
	}

	return d, nil
}

// SetSetting implements outputshare.Settings.
func (m *memSettings) SetSetting(
	_ context.Context, ns namespace.Name, d outputshare.Default, fence namespace.Fence, e audit.Entry,
) error {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return outputshare.ErrInvalid
	}

	if err := d.Validate(); err != nil {
		return err
	}

	if fence.Namespace != ns || e.Namespace != ns {
		return namespace.ErrFenced
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	space, ok := m.spaces[ns]
	if !ok {
		return namespace.ErrNotFound
	}

	if space.version != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	// A valid value replaces a stored value that fails Validate.
	space.setting = string(d)
	space.audited = append(space.audited, e.Action)

	return nil
}

// harness returns the harness of the cases of the setting over m.
func (m *memSettings) harness() outputsharetest.Harness {
	return outputsharetest.Harness{
		Store: nil, Checker: nil, Attachments: nil, States: nil, Settings: m,
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			m.spaces[namespace.Name(name)] = &memNamespace{
				setting: string(outputshare.DefaultNobody), audited: nil, version: 3,
			}

			return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 3}
		},
		Unit: nil, UnitFault: nil, Authority: nil, Profile: nil, Attach: nil, SaveState: nil, Grant: nil, Job: nil,
		Disable: nil,
		Audited: func(t *testing.T, ns string) []string {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			return append([]string(nil), m.spaces[namespace.Name(ns)].audited...)
		},
		Calls: nil, StoreRaw: nil,
		SettingRaw: func(t *testing.T, ns namespace.Name, value string) {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			m.spaces[ns].setting = value
		},
	}
}
