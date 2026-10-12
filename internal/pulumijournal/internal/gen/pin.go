package gen

// The input is apitype/journal.go of pulumi/pulumi at the pinned commit. The
// copy in testdata/ must have the pinned SHA-256 (ADR 43, decisions 8.1 and
// 8.2). A change of the pin is a change of this file, the copy and the
// generated file, in one pull request (ADR 43, decision 8.5).
const (
	// Commit is the pinned commit of pulumi/pulumi.
	Commit = "128d3f2ec94e147915a6a928fdf7449d561d560e"
	// Tag is the release tag of Commit.
	Tag = "v3.268.0"
	// InputSHA256 is the hex SHA-256 of testdata/journal.go.
	InputSHA256 = "088753b1c35890644e63168c05e35fe2c88751f6317418a6db65fdf8455464cc"
)
