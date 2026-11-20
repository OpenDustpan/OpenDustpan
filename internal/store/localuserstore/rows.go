package localuserstore

import "strings"

// HostileRow is one row that the CHECK constraints of the local user tables
// must refuse, for the tests of each adapter: the database is the last layer
// when every check of the code is skipped.
type HostileRow struct {
	Name         string
	UserName     string
	PasswordHash string
	SetAtMs      int64
	Change       bool
}

// The values of the rows.
const (
	validUserName    = "alice"
	longUserName     = 65
	longHashTailSize = 256
)

// validHash is a hash that the constraints accept.
const validHash = "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// HostileRows returns the rows that the local user table must refuse. Each
// row is valid but for one value.
func HostileRows() []HostileRow {
	return []HostileRow{
		{Name: "an empty user name", UserName: "", PasswordHash: validHash, SetAtMs: 1, Change: false},
		{Name: "an upper-case user name", UserName: "Alice", PasswordHash: validHash, SetAtMs: 1, Change: false},
		{Name: "a user name with a space", UserName: "al ice", PasswordHash: validHash, SetAtMs: 1, Change: false},
		{Name: "a user name with a line feed", UserName: "alice\n", PasswordHash: validHash, SetAtMs: 1, Change: false},
		{
			Name: "a user name that starts with a dot", UserName: ".alice", PasswordHash: validHash, SetAtMs: 1,
			Change: false,
		},
		{
			Name:         "a user name of 65 characters",
			UserName:     strings.Repeat("a", longUserName),
			PasswordHash: validHash,
			SetAtMs:      1,
			Change:       false,
		},
		{
			Name: "a user name with a letter outside ASCII", UserName: "alicé", PasswordHash: validHash, SetAtMs: 1,
			Change: false,
		},
		{
			Name:         "a hash of another algorithm",
			UserName:     validUserName,
			PasswordHash: "$2b$10$abc",
			SetAtMs:      1,
			Change:       false,
		},
		{Name: "a plain text password", UserName: validUserName, PasswordHash: "hunter2", SetAtMs: 1, Change: false},
		{
			Name: "a hash longer than 256 bytes", UserName: validUserName,
			PasswordHash: validHash + strings.Repeat("A", longHashTailSize), SetAtMs: 1, Change: false,
		},
		{Name: "a set time with no hash", UserName: validUserName, PasswordHash: "", SetAtMs: 1, Change: false},
		{Name: "change required with no hash", UserName: validUserName, PasswordHash: "", SetAtMs: 0, Change: true},
	}
}

// ValidRow returns a row that the constraints accept, so that a test can
// show that its insert works.
func ValidRow() HostileRow {
	return HostileRow{Name: "a valid row", UserName: validUserName, PasswordHash: validHash, SetAtMs: 1, Change: false}
}
