package cli

import (
	"regexp"
	"strings"
	"sync/atomic"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// brandingPresetKey is the key of the branding preset in the settings
// resource and in the configuration file.
const brandingPresetKey = "branding.preset"

// brandingPresetPattern is the form of the name of a preset: lower-case
// letters, digits and hyphens, at most 32 characters.
var brandingPresetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// brandingPresetError is the reason that the API gives for a name that is not a
// preset name. It holds no value.
type brandingPresetError struct{}

func (brandingPresetError) Error() string {
	return "a preset name has lower-case letters, digits and hyphens, and at most 32 characters"
}

// SettingDeclarations answers the declarations of the settings resource of
// the installation: each serve setting that the configuration file can set,
// and the settings that the database layer can hold.
//
// A serve setting is static only: a flag, a variable or the file sets it, and
// a change takes effect at the next start. A change through the API answers a
// conflict for it. A serve setting whose file key a database declaration
// already declares is not declared again: that declaration gives the key its
// static layer, so a flag, a variable or the file can lock it.
func SettingDeclarations() []setting.Declaration {
	var out []setting.Declaration

	held := localuser.SettingDeclarations()
	held = append(held, session.SettingDeclarations()...)
	held = append(held, access.SettingDeclarations()...)
	held = append(held, plainconn.SettingDeclarations()...)
	held = append(held, principal.RemovalAlertSettingDeclarations()...)
	held = append(held, brandingPresetDeclaration())

	declared := make(map[string]bool, len(held))
	for _, d := range held {
		if d.Database {
			declared[d.Key] = true
		}
	}

	for _, s := range ServeSettings() {
		if s.File == "" || declared[s.File] {
			continue
		}

		out = append(out, setting.Declaration{
			Key: s.File, Level: setting.LevelInstallation, Kind: kindOf(s.Kind), Default: defaultOf(s),
			Secret: s.Sensitive, Database: false, StaticCredentialHost: s.StaticCredentialHost,
			Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil, Doc: docOf(s),
		})
	}

	return append(out, held...)
}

// brandingPresetDeclaration returns the declaration of the branding preset,
// which the database layer holds.
func brandingPresetDeclaration() setting.Declaration {
	return setting.Declaration{
		Key: brandingPresetKey, Level: setting.LevelInstallation, Kind: setting.KindString, Default: "",
		Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive, ReducesSecurity: nil,
		Check: func(value string) error {
			if value != "" && !brandingPresetPattern.MatchString(value) {
				return brandingPresetError{}
			}

			return nil
		},
		Doc: "The name of the branding preset of the installation. Empty means the standard palette.",
	}
}

// kindOf converts the kind of a flag to the kind of a setting.
func kindOf(k Kind) setting.Kind {
	switch k {
	case KindBool:
		return setting.KindBool
	case KindInt:
		return setting.KindInt
	case KindString:
		return setting.KindString
	}

	return setting.KindString
}

// defaultOf answers the default that the resource shows: none for a sensitive
// setting.
func defaultOf(s *Setting) string {
	if s.Sensitive {
		return ""
	}

	return s.Default
}

// docOf answers the one-line description of s for the resource: the help
// text of the flag, else the description of the variable.
func docOf(s *Setting) string {
	text := s.Help
	if text == "" {
		text = s.Doc
	}

	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "The setting " + s.Name() + "."
	}

	return text
}

// StaticLayer is the static layer of the settings resource: the resolved
// serve settings of this process. Replace swaps in the settings of a reload.
// It is safe for concurrent use.
type StaticLayer struct {
	values atomic.Pointer[map[string]setting.StaticValue]
}

var _ setting.Static = (*StaticLayer)(nil)

// NewStaticLayer returns a layer for resolved.
func NewStaticLayer(resolved []Resolved) *StaticLayer {
	layer := &StaticLayer{values: atomic.Pointer[map[string]setting.StaticValue]{}}
	layer.Replace(resolved)

	return layer
}

// Replace swaps in the layer for resolved.
func (l *StaticLayer) Replace(resolved []Resolved) {
	values := make(map[string]setting.StaticValue, len(resolved))

	for i := range resolved {
		r := &resolved[i]
		if r.Setting.File == "" || r.Source == SourceDefault {
			continue
		}

		value := r.Value
		if r.Setting.Sensitive {
			// Resolve already replaced the value with set or not set. The
			// layer never holds the text of a sensitive value.
			value = ""
		}

		values[r.Setting.File] = setting.StaticValue{Source: sourceOf(r.Source), Value: value, By: byOf(r)}
	}

	l.values.Store(&values)
}

// Lookup implements setting.Static.
func (l *StaticLayer) Lookup(key string) (setting.StaticValue, bool) {
	values := l.values.Load()
	if values == nil {
		return setting.StaticValue{Source: "", Value: "", By: ""}, false
	}

	value, ok := (*values)[key]

	return value, ok
}

// sourceOf converts the layer of a resolved setting to a source.
func sourceOf(s Source) setting.Source {
	switch s {
	case SourceFlag:
		return setting.SourceFlag
	case SourceEnv:
		return setting.SourceEnv
	case SourceFile:
		return setting.SourceFile
	case SourceDefault:
		return setting.SourceDefault
	}

	return setting.SourceDefault
}

// byOf says which flag, variable or file key gave the value of r.
func byOf(r *Resolved) string {
	switch r.Source {
	case SourceFlag:
		return "the flag --" + r.Setting.Flag
	case SourceEnv:
		return "the environment variable " + r.Setting.Env
	case SourceFile:
		return "the key " + r.Setting.File + " of the configuration file"
	case SourceDefault:
		return "the default"
	default:
		return "the static layer"
	}
}
