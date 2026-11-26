package outputsharetest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// actionManageSharing is the action of the audit entry of the rule of a new
// unit.
const actionManageSharing = "unit:manage-sharing"

// newUnitName returns a unit name of the tool that the hooks accept.
func newUnitName(tool string) string {
	if tool == ToolPulumi {
		return "proj/stk"
	}

	return "ws"
}

// newUnitTools returns the tools whose stores make units.
func newUnitTools() []string {
	return []string{ToolOpenTofu, ToolPulumi}
}

// needNewUnitHooks skips the case when the harness cannot make units or read
// the rules.
func needNewUnitHooks(t *testing.T, h Harness) {
	t.Helper()

	if h.Unit == nil || h.Store == nil || h.Settings == nil || h.Namespace == nil {
		t.Skip("the harness has no stores of the tools")
	}
}

// wantRules fails the test unless the unit has exactly want rules.
func wantRules(t *testing.T, h Harness, p outputshare.Placement, want int) []outputshare.Rule {
	t.Helper()

	rules, err := h.Store.List(t.Context(), p.Unit)
	if err != nil {
		t.Fatalf("List(%s): %v", p.Unit, err)
	}

	if len(rules) != want {
		t.Fatalf("List(%s) = %d rules, want %d", p.Unit, len(rules), want)
	}

	return rules
}

// testNewUnitNobody proves that a namespace with the setting nobody gives a new
// unit no rule.
func testNewUnitNobody(t *testing.T, h Harness) {
	t.Helper()
	needNewUnitHooks(t, h)

	h.Namespace(t, acme.String())
	wantSetting(t, h, acme, outputshare.DefaultNobody)

	for _, tool := range newUnitTools() {
		p := h.Unit(t, tool, acme, newUnitName(tool), "")
		wantRules(t, h, p, 0)
	}
}

// testNewUnitNamespace proves that a namespace with the setting namespace gives
// a new unit one rule whose consumer is the namespace of the unit, and that the
// server is the actor of the rule.
func testNewUnitNamespace(t *testing.T, h Harness) {
	t.Helper()
	needNewUnitHooks(t, h)

	fence := h.Namespace(t, acme.String())
	setSetting(t, h, fence, outputshare.DefaultNamespace)

	for _, tool := range newUnitTools() {
		p := h.Unit(t, tool, acme, newUnitName(tool), "")
		rules := wantRules(t, h, p, 1)

		r := rules[0]
		want := outputshare.Consumer{Kind: outputshare.ConsumerNamespace, Namespace: acme, ID: ""}

		if r.Consumer != want {
			t.Fatalf("the rule of %s has the consumer %s, want %s", p.Unit, r.Consumer, want)
		}

		if r.Producer != p.Unit {
			t.Fatalf("the rule of %s has the producer %s", p.Unit, r.Producer)
		}

		if r.CreatedBy != history.ServerSubject {
			t.Fatalf("the rule of %s has CreatedBy %q, want %q", p.Unit, r.CreatedBy, history.ServerSubject)
		}

		if err := r.Validate(); err != nil {
			t.Fatalf("the rule of %s fails Validate: %v", p.Unit, err)
		}

		got, err := h.Store.Get(t.Context(), p.Unit, r.ID)
		if err != nil || got.ID != r.ID {
			t.Fatalf("Get(%s, %s) = %v, %v, want the rule", p.Unit, r.ID, got.ID, err)
		}
	}
}

// testNewUnitAudit proves that the rule of a new unit has one audit entry in
// the namespace of the unit, and none in another namespace.
func testNewUnitAudit(t *testing.T, h Harness) {
	t.Helper()
	needNewUnitHooks(t, h)
	needHook(t, h, "Audited")

	fence := h.Namespace(t, acme.String())
	h.Namespace(t, globex.String())
	setSetting(t, h, fence, outputshare.DefaultNamespace)

	for _, tool := range newUnitTools() {
		beforeAcme := h.Audited(t, acme.String())
		beforeGlobex := h.Audited(t, globex.String())

		p := h.Unit(t, tool, acme, newUnitName(tool), "")

		afterAcme := h.Audited(t, acme.String())
		if len(afterAcme) != len(beforeAcme)+1 || afterAcme[len(afterAcme)-1] != actionManageSharing {
			t.Fatalf("audit actions of acme after the new %s unit = %q, want one more: %q",
				tool, afterAcme, actionManageSharing)
		}

		if afterGlobex := h.Audited(t, globex.String()); !slices.Equal(afterGlobex, beforeGlobex) {
			t.Fatalf("audit actions of globex = %q, want %q: the rule is in acme only", afterGlobex, beforeGlobex)
		}

		if h.AuditEntries != nil {
			wantNewUnitEntry(t, h, p, tool)
		}
	}
}

// wantNewUnitEntry fails the test unless the last audit entry of acme is the
// entry of the rule of the new unit p, with each field that names the actor,
// the cause and the reason. The plain create of the hooks has no audit entry of
// its own, so the request ID is empty and the channel is the API.
func wantNewUnitEntry(t *testing.T, h Harness, p outputshare.Placement, tool string) {
	t.Helper()

	entries := h.AuditEntries(t, acme.String())
	if len(entries) == 0 {
		t.Fatalf("the %s unit has no audit entry in acme", tool)
	}

	e := entries[len(entries)-1]
	rules := wantRules(t, h, p, 1)

	checks := []struct {
		name string
		ok   bool
		got  string
	}{
		{"Subject", e.Subject == history.ServerSubject, e.Subject},
		{"Action", e.Action == actionManageSharing, e.Action},
		{"Object", e.Object == p.Unit.String(), e.Object},
		{"ObjectID", e.ObjectID == string(rules[0].ID), e.ObjectID},
		{"RequestID", e.RequestID == "", e.RequestID},
		{"Channel", e.Channel == audit.ChannelAPI, string(e.Channel)},
		{"Reason", strings.Contains(e.Reason, acme.String()) && strings.Contains(e.Reason, "created by"), e.Reason},
	}

	for _, c := range checks {
		if !c.ok {
			t.Fatalf("the audit entry of the rule of the new %s unit has %s = %q", tool, c.name, c.got)
		}
	}
}

// laterUnitName returns a second unit name of the tool that the hooks accept.
func laterUnitName(tool string) string {
	if tool == ToolPulumi {
		return "proj/later"
	}

	return "later"
}

// testSetSettingKeepsUnits proves that a change of the setting changes no rule
// of a unit that exists, and that only a later unit uses the new value.
func testSetSettingKeepsUnits(t *testing.T, h Harness) {
	t.Helper()
	needNewUnitHooks(t, h)

	fence := h.Namespace(t, acme.String())
	setSetting(t, h, fence, outputshare.DefaultNamespace)

	for _, tool := range newUnitTools() {
		first := h.Unit(t, tool, acme, newUnitName(tool), "")
		want := wantRules(t, h, first, 1)[0]

		setSetting(t, h, fence, outputshare.DefaultNobody)

		if got := wantRules(t, h, first, 1)[0]; got.ID != want.ID {
			t.Fatalf("the rule of %s after the setting nobody = %s, want %s", first.Unit, got.ID, want.ID)
		}

		later := h.Unit(t, tool, acme, laterUnitName(tool), "")
		wantRules(t, h, later, 0)

		setSetting(t, h, fence, outputshare.DefaultNamespace)

		wantRules(t, h, later, 0)
		wantRules(t, h, first, 1)
	}
}

// testNewUnitFault proves that a fault after the write of the rule and before
// the commit leaves no unit, no rule and no audit entry.
func testNewUnitFault(t *testing.T, h Harness) {
	t.Helper()
	needNewUnitHooks(t, h)
	needHook(t, h, "Audited")

	if h.UnitFault == nil {
		t.Skip("the adapter has no hook UnitFault")
	}

	fence := h.Namespace(t, acme.String())
	setSetting(t, h, fence, outputshare.DefaultNamespace)

	for _, tool := range newUnitTools() {
		before := h.Audited(t, acme.String())

		if err := h.UnitFault(t, tool, acme, newUnitName(tool)); err == nil {
			t.Fatalf("the create of the %s unit with a fault answered no error", tool)
		}

		if after := h.Audited(t, acme.String()); !slices.Equal(after, before) {
			t.Fatalf("audit actions after the faulted %s create = %q, want %q", tool, after, before)
		}

		// The same name is free, so no unit stayed, and the new unit has the
		// one rule of its own create.
		p := h.Unit(t, tool, acme, newUnitName(tool), "")
		wantRules(t, h, p, 1)
	}
}

// testNewUnitInvalidSetting proves that a stored setting that fails Validate
// refuses the create and writes nothing.
func testNewUnitInvalidSetting(t *testing.T, h Harness) {
	t.Helper()
	needNewUnitHooks(t, h)
	needHook(t, h, "Audited")

	if h.SettingRaw == nil || h.UnitFault == nil {
		t.Skip("the adapter has no hook SettingRaw or UnitFault")
	}

	fence := h.Namespace(t, acme.String())
	h.SettingRaw(t, acme, "every one")

	for _, tool := range newUnitTools() {
		before := h.Audited(t, acme.String())

		err := h.UnitFault(t, tool, acme, newUnitName(tool))
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("the create of the %s unit with the setting %q = %v, want ErrInvalid", tool, "every one", err)
		}

		// The answer names the namespace and the one command that helps.
		wantCommand := "GET /api/dustpan/v1/namespaces/" + acme.String() + "/output-sharing"
		if !strings.Contains(err.Error(), "namespace "+acme.String()) || !strings.Contains(err.Error(), wantCommand) {
			t.Fatalf("the error of the refused %s create = %q, want the namespace %s and %q",
				tool, err, acme, wantCommand)
		}

		if after := h.Audited(t, acme.String()); !slices.Equal(after, before) {
			t.Fatalf("audit actions after the refused %s create = %q, want %q", tool, after, before)
		}
	}

	// A valid setting repairs the row, and the same names are free: no unit
	// stayed from the refused creates.
	setSetting(t, h, fence, outputshare.DefaultNobody)

	for _, tool := range newUnitTools() {
		p := h.Unit(t, tool, acme, newUnitName(tool), "")
		wantRules(t, h, p, 0)
	}
}
