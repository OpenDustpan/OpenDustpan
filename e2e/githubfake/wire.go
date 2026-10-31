package githubfake

import "encoding/json"

// The types below are the JSON bodies that the fake answers and reads.

type wireMessage struct {
	Message string `json:"message"`
}

type wireAccount struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type wireInstallation struct {
	ID          json.Number       `json:"id"`
	Account     wireAccount       `json:"account"`
	Permissions map[string]string `json:"permissions"`
}

type wirePerson struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type wireCommitBody struct {
	Message   string     `json:"message"`
	Author    wirePerson `json:"author"`
	Committer wirePerson `json:"committer"`
}

type wireParent struct {
	SHA string `json:"sha"`
}

type wireCommit struct {
	SHA     string         `json:"sha"`
	Commit  wireCommitBody `json:"commit"`
	Parents []wireParent   `json:"parents"`
}

// wireEntry is a file or a directory in the answer of the contents call.
// Encoding and Content are nil in a directory listing.
type wireEntry struct {
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	SHA      string  `json:"sha"`
	Size     int     `json:"size"`
	Encoding *string `json:"encoding,omitempty"`
	Content  *string `json:"content,omitempty"`
}

type wireBlob struct {
	SHA      string `json:"sha"`
	Size     int    `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type wireGQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireGQLCommit struct {
	OID           string `json:"oid"`
	CommittedDate string `json:"committedDate"`
}

type wireGQLPayload struct {
	Commit wireGQLCommit `json:"commit"`
}

type wireGQLData struct {
	CreateCommitOnBranch *wireGQLPayload `json:"createCommitOnBranch"`
}

type wireGQLResponse struct {
	Data   *wireGQLData   `json:"data,omitempty"`
	Errors []wireGQLError `json:"errors,omitempty"`
}

type wireErrorCode struct {
	Code string `json:"code"`
}

type wireTooLarge struct {
	Message string          `json:"message"`
	Errors  []wireErrorCode `json:"errors"`
}
