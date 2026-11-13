// Package plainconn holds the installation setting that allows a connection
// with no TLS, and the read of it that fails closed. It is apart from
// package egress so that the packages that link egress do not link the
// settings service.
package plainconn

import (
	"context"

	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingAllowPlainConnections is the key of the installation setting that
// allows a connection with no TLS to an LDAP directory or an SMTP server.
// The setting is off by default: a plain connection to an address that is
// not loopback is then refused.
const SettingAllowPlainConnections = "egress.allowPlainConnections"

// The text forms of the bool setting.
const (
	textTrue  = "true"
	textFalse = "false"
)

// SettingDeclarations returns the declarations of the settings of the
// outbound connections, for the settings resource of the installation. The
// database layer holds each one, and the static layer can lock each one.
func SettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingAllowPlainConnections, Level: setting.LevelInstallation, Kind: setting.KindBool,
			Default: textFalse, Secret: false, Database: true, StaticCredentialHost: false,
			Apply: setting.ApplyLive, Check: nil,
			// A change from off to on lets an LDAP bind or an SMTP login send
			// its password with no encryption. A value that is not a bool
			// reduces security too, so the caller fails closed.
			ReducesSecurity: func(old, next string) bool {
				if (old != textTrue && old != textFalse) || (next != textTrue && next != textFalse) {
					return true
				}

				return old == textFalse && next == textTrue
			},
			Doc: "Allow LDAP and SMTP connections with no TLS. An LDAP bind or an SMTP login then sends its " +
				"password with no encryption.",
		},
	}
}

// PlainRead reads the effective item of a setting key of the installation.
// The settings service implements it.
type PlainRead interface {
	// Effective returns the setting key of scope as consumers read it: the
	// effective value and its source.
	Effective(ctx context.Context, scope setting.Scope, key string) (setting.Item, error)
}

// The settings service of the installation implements PlainRead.
var _ PlainRead = (*setting.Service)(nil)

// PlainAllowed reports whether the installation allows a connection with no
// TLS. It reads the effective value of SettingAllowPlainConnections through
// read. It fails closed: a read error, an unknown key, a missing value or a
// value that is not exactly true gives false.
func PlainAllowed(ctx context.Context, read PlainRead) bool {
	if read == nil {
		return false
	}

	// A nil settings service in the interface is not a nil interface, and its
	// Effective panics. Refuse it as no reader.
	if svc, ok := read.(*setting.Service); ok && svc == nil {
		return false
	}

	item, err := read.Effective(ctx, setting.InstallationScope(), SettingAllowPlainConnections)
	if err != nil {
		return false
	}

	return item.Value == textTrue
}
