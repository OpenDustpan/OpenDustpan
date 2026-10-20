package principaltest

// DevSubjectCase is one row of the shared table of subjects. Every matcher of
// the reserved dev prefix must give Dev for Subject: principal.IsDevSubject
// and the SQL match of each store adapter.
type DevSubjectCase struct {
	Name    string
	Subject string
	// Dev is true when the first 4 bytes of Subject are "dev:" with any
	// ASCII letter case.
	Dev bool
}

// DevSubjectCases returns the shared table of subjects. Only the ASCII
// letters fold, so a look-alike that Unicode case folding would match (the
// long s U+017F and the Kelvin sign U+212A fold to ASCII letters) is not a
// dev subject.
func DevSubjectCases() []DevSubjectCase {
	return []DevSubjectCase{
		{Name: "dev admin", Subject: "dev:admin", Dev: true},
		{Name: "upper case with colon only", Subject: "DEV:", Dev: true},
		{Name: "mixed case with name", Subject: "Dev:Admin", Dev: true},
		{Name: "alternating case", Subject: "dEv:", Dev: true},
		{Name: "prefix only", Subject: "dev:", Dev: true},
		{Name: "prefix with a long tail", Subject: "dEV:x/y@z", Dev: true},
		{Name: "no colon", Subject: "dev", Dev: false},
		{Name: "no colon with tail", Subject: "devops", Dev: false},
		{Name: "short", Subject: "de", Dev: false},
		{Name: "empty", Subject: "", Dev: false},
		{Name: "leading space", Subject: " dev:x", Dev: false},
		{Name: "prefix in the middle", Subject: "x-dev:admin", Dev: false},
		{Name: "other prefix", Subject: "dew:admin", Dev: false},
		{Name: "fullwidth letters", Subject: "ｄｅｖ:admin", Dev: false},
		{Name: "fullwidth colon", Subject: "dev：admin", Dev: false},
		{Name: "Cyrillic e", Subject: "dеv:admin", Dev: false},
		{Name: "long s in place of a letter", Subject: "ſev:admin", Dev: false},
		{Name: "Kelvin sign in a tail", Subject: "devK", Dev: false},
		{Name: "Kelvin sign for k in another prefix", Subject: "Kev:admin", Dev: false},
		{Name: "long s after the prefix", Subject: "dev:ſ", Dev: true},
	}
}
