package accesstoken

import "time"

// IssueTokenForTest calls the private record constructor of the service with a
// kind of the caller's choice, so that a test can send a hostile kind to this
// layer when every other layer fails.
func IssueTokenForTest(s *Service, k Kind) (Token, error) {
	n, err := s.newToken(tokenSpec{
		at: time.Unix(0, 0), expiresAt: time.Unix(3600, 0),
		principalID: "prn-1", name: "n", kind: k, createdBy: "admin",
	})

	return n.token, err
}
