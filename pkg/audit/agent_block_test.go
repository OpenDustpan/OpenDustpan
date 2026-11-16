package audit_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

// fullArgs answers the origin arguments of agentEntry.
func fullArgs() audit.OriginArgs {
	return audit.OriginArgs{
		Run:          "run-1",
		Session:      "run-1",
		Tool:         "read",
		ArgsDigest:   "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		BreakGlass:   false,
		SecretStage:  "open",
		DelegationID: "dlg-1",
	}
}

// mustOrigin builds the agent origin of a test entry and fails the test
// when its fields are not valid.
func mustOrigin(t *testing.T, args audit.OriginArgs) *audit.Origin {
	t.Helper()

	origin, err := audit.NewOrigin(args)
	if err != nil {
		t.Fatalf("new origin: %v", err)
	}

	return origin
}

func agentEntry(t *testing.T) audit.Entry {
	t.Helper()

	e := testEntry()
	e.Channel = audit.ChannelAgent
	e.Origin = mustOrigin(t, fullArgs())

	return e
}

// TestComputeHashAgentBlock proves that an entry with the block hashes
// differently from the same entry without it, that each agent field moves
// the hash, that swapping two fields moves it, and that a block with one
// field set still holds all seven fields.
func TestComputeHashAgentBlock(t *testing.T) {
	t.Parallel()

	plain := testEntry()
	agent := agentEntry(t)

	if !agent.HasAgentBlock() {
		t.Fatal("HasAgentBlock = false, want true for an entry with agent data")
	}

	if plain.HasAgentBlock() {
		t.Fatal("HasAgentBlock = true, want false for an entry without agent data")
	}

	plainHash := audit.ComputeHash(plain, "")
	agentHash := audit.ComputeHash(agent, "")

	if plainHash == agentHash {
		t.Fatal("hash of an entry with the agent block equals the hash without it")
	}

	set := func(change func(*audit.OriginArgs)) *audit.Origin {
		args := fullArgs()
		change(&args)

		return mustOrigin(t, args)
	}

	setters := map[string]*audit.Origin{
		"agent run":        set(func(args *audit.OriginArgs) { args.Run = "other" }),
		"agent session":    set(func(args *audit.OriginArgs) { args.Session = "other" }),
		"tool":             set(func(args *audit.OriginArgs) { args.Tool = "other" }),
		"arguments digest": set(func(args *audit.OriginArgs) { args.ArgsDigest = "other" }),
		"secret stage":     set(func(args *audit.OriginArgs) { args.SecretStage = "other" }),
		"delegation ID":    set(func(args *audit.OriginArgs) { args.DelegationID = "other" }),
	}

	for name, origin := range setters {
		changed := testEntry()
		changed.Channel = audit.ChannelAgent
		changed.Origin = origin

		if got := audit.ComputeHash(changed, ""); got == agentHash {
			t.Errorf("hash is unchanged when the %s differs", name)
		}
	}

	flipped := testEntry()
	flipped.Channel = audit.ChannelAgent
	flippedArgs := fullArgs()
	flippedArgs.BreakGlass = true
	flipped.Origin = mustOrigin(t, flippedArgs)

	if got := audit.ComputeHash(flipped, ""); got == agentHash {
		t.Error("hash is unchanged when break-glass differs")
	}

	swapped := testEntry()
	swapped.Channel = audit.ChannelAgent
	first := fullArgs()
	first.Session = "ses-1"
	swapped.Origin = mustOrigin(t, first)
	swappedHash := audit.ComputeHash(swapped, "")
	second := fullArgs()
	second.Run, second.Session = first.Session, first.Run
	swapped.Origin = mustOrigin(t, second)

	if got := audit.ComputeHash(swapped, ""); got == swappedHash {
		t.Error("hash is unchanged when agent run and agent session are swapped")
	}

	oneField := testEntry()
	oneField.Origin = mustOrigin(t, audit.OriginArgs{
		Run: "run-1", Session: "", Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "", DelegationID: "",
	})

	if !oneField.HasAgentBlock() {
		t.Fatal("HasAgentBlock = false, want true when only the agent run is set")
	}

	if got, want := audit.ComputeHash(oneField, ""), audit.ComputeHash(testEntry(), ""); got == want {
		t.Fatal("hash of an entry with only the agent run set equals the hash without the block")
	}
}

// TestComputeHashDiffersForEachAgentField proves that each agent field is
// covered by the hash: clearing any one field of a full block changes it.
func TestComputeHashDiffersForEachAgentField(t *testing.T) {
	t.Parallel()

	glass := fullArgs()
	glass.BreakGlass = true
	full := agentEntry(t)
	full.Origin = mustOrigin(t, glass)
	fullHash := audit.ComputeHash(full, "")

	digest := glass.ArgsDigest
	cleared := []audit.OriginArgs{
		{
			Run:          "",
			Session:      "run-1",
			Tool:         "read",
			ArgsDigest:   digest,
			BreakGlass:   true,
			SecretStage:  "open",
			DelegationID: "dlg-1",
		},
		{
			Run:          "run-1",
			Session:      "",
			Tool:         "read",
			ArgsDigest:   digest,
			BreakGlass:   true,
			SecretStage:  "open",
			DelegationID: "dlg-1",
		},
		{
			Run:          "run-1",
			Session:      "run-1",
			Tool:         "",
			ArgsDigest:   digest,
			BreakGlass:   true,
			SecretStage:  "open",
			DelegationID: "dlg-1",
		},
		{
			Run:          "run-1",
			Session:      "run-1",
			Tool:         "read",
			ArgsDigest:   "",
			BreakGlass:   true,
			SecretStage:  "open",
			DelegationID: "dlg-1",
		},
		{
			Run:          "run-1",
			Session:      "run-1",
			Tool:         "read",
			ArgsDigest:   digest,
			BreakGlass:   false,
			SecretStage:  "open",
			DelegationID: "dlg-1",
		},
		{
			Run:          "run-1",
			Session:      "run-1",
			Tool:         "read",
			ArgsDigest:   digest,
			BreakGlass:   true,
			SecretStage:  "",
			DelegationID: "dlg-1",
		},
		{
			Run:          "run-1",
			Session:      "run-1",
			Tool:         "read",
			ArgsDigest:   digest,
			BreakGlass:   true,
			SecretStage:  "open",
			DelegationID: "",
		},
	}

	for i, args := range cleared {
		e := full
		e.Origin = mustOrigin(t, args)

		if got := audit.ComputeHash(e, ""); got == fullHash {
			t.Errorf("field %d: hash is unchanged when the field is cleared", i)
		}
	}
}

// TestNewOriginRefusesHostileFields proves that an agent field with a
// control character or with bytes that are not valid UTF-8 is refused at
// construction: NewOrigin answers ErrInvalidOrigin and no origin.
func TestNewOriginRefusesHostileFields(t *testing.T) {
	t.Parallel()

	hostile := []struct {
		name string
		args audit.OriginArgs
	}{
		{
			name: "control character in the agent run",
			args: audit.OriginArgs{
				Run:          "run-\x00-1",
				Session:      "run-1",
				Tool:         "",
				ArgsDigest:   "",
				BreakGlass:   false,
				SecretStage:  "",
				DelegationID: "",
			},
		},
		{
			name: "control character in the agent session",
			args: audit.OriginArgs{
				Run:          "run-1",
				Session:      "ses-\x7f-1",
				Tool:         "",
				ArgsDigest:   "",
				BreakGlass:   false,
				SecretStage:  "",
				DelegationID: "",
			},
		},
		{
			name: "control character in the tool",
			args: audit.OriginArgs{
				Run:          "run-1",
				Session:      "run-1",
				Tool:         "re\x1bad",
				ArgsDigest:   "",
				BreakGlass:   false,
				SecretStage:  "",
				DelegationID: "",
			},
		},
		{
			name: "control character in the arguments digest",
			args: audit.OriginArgs{
				Run:          "run-1",
				Session:      "run-1",
				Tool:         "",
				ArgsDigest:   "ab\ncd",
				BreakGlass:   false,
				SecretStage:  "",
				DelegationID: "",
			},
		},
		{
			name: "control character in the secret stage",
			args: audit.OriginArgs{
				Run:          "run-1",
				Session:      "run-1",
				Tool:         "",
				ArgsDigest:   "",
				BreakGlass:   false,
				SecretStage:  "o\x1bpen",
				DelegationID: "",
			},
		},
		{
			name: "control character in the delegation ID",
			args: audit.OriginArgs{
				Run:          "run-1",
				Session:      "run-1",
				Tool:         "",
				ArgsDigest:   "",
				BreakGlass:   false,
				SecretStage:  "",
				DelegationID: "dl\x00g-1",
			},
		},
		{
			name: "invalid UTF-8 in the agent run",
			args: audit.OriginArgs{
				Run:          "run-\xff-1",
				Session:      "run-1",
				Tool:         "",
				ArgsDigest:   "",
				BreakGlass:   false,
				SecretStage:  "",
				DelegationID: "",
			},
		},
	}

	for _, tt := range hostile {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			origin, err := audit.NewOrigin(tt.args)
			if !errors.Is(err, audit.ErrInvalidOrigin) || origin != nil {
				t.Fatalf("new origin accepted %s: origin %+v, err %v", tt.name, origin, err)
			}
		})
	}
}

// TestNewOriginRefusesOversizedField proves that an agent field over
// 256 bytes is refused at construction, and that a field of exactly
// 256 bytes is kept.
func TestNewOriginRefusesOversizedField(t *testing.T) {
	t.Parallel()

	fields := []struct {
		name   string
		change func(*audit.OriginArgs, string)
	}{
		{name: "agent run", change: func(args *audit.OriginArgs, long string) { args.Run = long }},
		{name: "agent session", change: func(args *audit.OriginArgs, long string) { args.Session = long }},
		{name: "tool", change: func(args *audit.OriginArgs, long string) { args.Tool = long }},
		{name: "arguments digest", change: func(args *audit.OriginArgs, long string) { args.ArgsDigest = long }},
		{name: "secret stage", change: func(args *audit.OriginArgs, long string) { args.SecretStage = long }},
		{name: "delegation ID", change: func(args *audit.OriginArgs, long string) { args.DelegationID = long }},
	}

	base := audit.OriginArgs{
		Run:          "run-1",
		Session:      "ses-1",
		Tool:         "",
		ArgsDigest:   "",
		BreakGlass:   false,
		SecretStage:  "",
		DelegationID: "",
	}

	for _, field := range fields {
		args := base
		field.change(&args, strings.Repeat("a", audit.MaxAgentFieldLen+1))

		origin, err := audit.NewOrigin(args)
		if !errors.Is(err, audit.ErrInvalidOrigin) || origin != nil {
			t.Errorf("new origin accepted an overlong %s", field.name)
		}
	}

	origin, err := audit.NewOrigin(audit.OriginArgs{
		Run: strings.Repeat("a", audit.MaxAgentFieldLen), Session: "ses-1",
		Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "", DelegationID: "",
	})
	if err != nil || origin == nil {
		t.Fatalf("new origin refused a %d-byte agent run: %v", audit.MaxAgentFieldLen, err)
	}
}

// TestNewOriginRefusesPartialOrigin proves that agent details without the
// run or the session that identifies the call are refused: the tool, the
// arguments digest, the secret stage and the delegation alone name no call.
func TestNewOriginRefusesPartialOrigin(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		args audit.OriginArgs
	}{
		{name: "tool without identity", args: audit.OriginArgs{Run: "", Session: "", Tool: "read", ArgsDigest: "", BreakGlass: false, SecretStage: "", DelegationID: ""}},
		{name: "arguments digest without identity", args: audit.OriginArgs{Run: "", Session: "", Tool: "", ArgsDigest: "abcd", BreakGlass: false, SecretStage: "", DelegationID: ""}},
		{name: "secret stage without identity", args: audit.OriginArgs{Run: "", Session: "", Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "open", DelegationID: ""}},
		{name: "delegation without identity", args: audit.OriginArgs{Run: "", Session: "", Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "", DelegationID: "dlg-1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			origin, err := audit.NewOrigin(tt.args)
			if !errors.Is(err, audit.ErrInvalidOrigin) || origin != nil {
				t.Fatalf("new origin accepted %s: origin %+v, err %v", tt.name, origin, err)
			}
		})
	}
}

// TestOriginNilIsNoBlock proves that an entry with a nil origin carries no
// agent block and hashes exactly as before the block existed.
func TestOriginNilIsNoBlock(t *testing.T) {
	t.Parallel()

	plain := testEntry()

	if plain.Origin != nil {
		t.Fatal("origin of an ordinary entry is not nil")
	}

	if plain.HasAgentBlock() {
		t.Fatal("HasAgentBlock = true, want false for an entry with a nil origin")
	}

	origin, err := audit.NewOrigin(audit.OriginArgs{
		Run: "", Session: "", Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "", DelegationID: "",
	})
	if err != nil || origin != nil {
		t.Fatalf("new origin of seven empty fields = %+v, %v, want (nil, nil)", origin, err)
	}
}

// TestEveryOriginFieldIsHashed proves that every field of audit.Origin is
// covered by the hash: setting each field of the struct alone (by
// reflection over the struct) moves the hash of the entry. A field that a
// later change adds but ComputeHash does not cover fails this test.
func TestEveryOriginFieldIsHashed(t *testing.T) {
	t.Parallel()

	base := reflect.TypeFor[audit.Origin]()
	if base.Kind() != reflect.Struct {
		t.Fatalf("audit.Origin is %s, want a struct", base.Kind())
	}

	plain := testEntry()
	plainHash := audit.ComputeHash(plain, "")

	for field := range base.Fields() {
		set := map[string]func(*audit.OriginArgs){
			"run":          func(args *audit.OriginArgs) { args.Run = "run-9" },
			"session":      func(args *audit.OriginArgs) { args.Session = "ses-9" },
			"tool":         func(args *audit.OriginArgs) { args.Tool = "exec" },
			"argsDigest":   func(args *audit.OriginArgs) { args.ArgsDigest = "digest-9" },
			"breakGlass":   func(args *audit.OriginArgs) { args.BreakGlass = true },
			"secretStage":  func(args *audit.OriginArgs) { args.SecretStage = "closed" },
			"delegationID": func(args *audit.OriginArgs) { args.DelegationID = "dlg-9" },
		}[field.Name]
		if set == nil {
			t.Fatalf("Origin holds a field %q with no setter: cover it in the hash", field.Name)
		}

		args := audit.OriginArgs{
			Run: "run-1", Session: "run-1", Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "",
			DelegationID: "",
		}
		set(&args)

		e := testEntry()
		e.Channel = audit.ChannelAgent
		e.Origin = mustOrigin(t, args)

		if got := audit.ComputeHash(e, ""); got == plainHash {
			t.Errorf("hash is unchanged when only the field %q is set", field.Name)
		}
	}
}

// TestChainCheckFailsOnChangedAgentField proves that changing an agent
// field of a stored entry breaks its hash, so the chain check finds it.
func TestChainCheckFailsOnChangedAgentField(t *testing.T) {
	t.Parallel()

	e := agentEntry(t)

	want := audit.ComputeHash(e, "")

	changed := e
	changedArgs := fullArgs()
	changedArgs.Tool = "write"
	changed.Origin = mustOrigin(t, changedArgs)

	if got := audit.ComputeHash(changed, ""); got == want {
		t.Fatal("hash is unchanged after the tool field changed")
	}

	if got := audit.ComputeStoredHash(e, "", "[]"); got == want && len(e.Changes) != 0 {
		t.Fatal("stored hash equals the entry hash for different changes text")
	}
}

// TestValidateRefusesEmptyOrigin proves that Entry.Validate refuses an entry
// whose Origin is a non-nil value that NewOrigin did not build: the zero
// value holds no run and no session.
func TestValidateRefusesEmptyOrigin(t *testing.T) {
	t.Parallel()

	e := testEntry()
	e.Origin = new(audit.Origin)

	if err := e.Validate(); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Validate with an empty origin = %v, want audit.ErrInvalidEntry", err)
	}
}
