package cli

import (
	"errors"
	"fmt"
	"strings"
)

// HostCreateTempAdminCommand answers the host row of the command table with
// run: a direct store command, not a client of the REST API. It reaches the
// database without the public listener, the identity provider or DNS, so an
// operator with host access recovers after every administrator is lost. The
// person comes from the command line, never from a configuration file, and
// no token is read from one either: the command mints the credential.
func HostCreateTempAdminCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandHost, commandCreateTempAdmin},
		Synopsis: "Create a temporary administrator for one named person.",
		Settings: SettingsFor("database", "reason", "recovery-mode", "disable-host-recovery"),
		Run:      run,
	}
}

// ErrBadBool reports a bool variable with a value that is not true, 1,
// false, 0 or empty.
var ErrBadBool = errors.New("not a bool value")

// HostRecoveryOff reads the off switch of the host command with lookup:
// true for `true` or `1`, false when the variable is unset, empty, `false`
// or `0`. Any other value is an error, so a typo in the switch never turns
// the command on.
func HostRecoveryOff(lookup LookupFunc) (bool, error) {
	return EnvBoolStrict(lookup, ByFlag("disable-host-recovery").Env)
}

// EnvWith reads the variable key with lookup, or answers fallback when it
// is unset, as Env does with the process environment.
func EnvWith(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok {
		return value
	}

	return fallback
}

// EnvBoolStrict reads the bool variable key with lookup: true for `true`
// or `1`, false when it is unset, empty, `false` or `0`, with no case and
// no surrounding space. Any other value returns ErrBadBool.
func EnvBoolStrict(lookup LookupFunc, key string) (bool, error) {
	raw, ok := lookup(key)
	if !ok {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case valueTrue, "1":
		return true, nil
	case "", "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s=%q: set true, 1, false or 0", ErrBadBool, key, raw)
	}
}
