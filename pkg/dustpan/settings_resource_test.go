package dustpan_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/setting"
)

func TestWithSettingsRefusesADeclarationThatIsNotValid(t *testing.T) {
	t.Parallel()

	p := newPorts(t)

	bad := []setting.Declaration{{
		Key: "Not A Key", Level: setting.LevelInstallation, Kind: setting.KindString, Default: "", Secret: false,
		Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive, Check: nil, ReducesSecurity: nil,
		Doc: "A setting.",
	}}

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn), dustpan.WithSettings(p.st.SettingStore(), bad, nil))

	if _, err := dustpan.NewServer(opts...); !errors.Is(err, setting.ErrInvalidDeclaration) {
		t.Fatalf("NewServer = %v, want ErrInvalidDeclaration", err)
	}
}

func TestWithSettingsAcceptsNoDeclaration(t *testing.T) {
	t.Parallel()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn), dustpan.WithSettings(p.st.SettingStore(), nil, nil))

	if _, err := dustpan.NewServer(opts...); err != nil {
		t.Fatalf("NewServer: %v", err)
	}
}
