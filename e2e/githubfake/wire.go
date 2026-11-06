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
	Message      string           `json:"message"`
	Author       wirePerson       `json:"author"`
	Committer    wirePerson       `json:"committer"`
	Verification wireVerification `json:"verification"`
}

type wireParent struct {
	SHA string `json:"sha"`
}

type wireCommit struct {
	SHA     string         `json:"sha"`
	Commit  wireCommitBody `json:"commit"`
	Parents []wireParent   `json:"parents"`
	// Author and Committer are the accounts of GitHub, or null for an email
	// that no account has.
	Author    *wireLogin `json:"author"`
	Committer *wireLogin `json:"committer"`
}

type wireLogin struct {
	Login string `json:"login"`
}

type wireVerification struct {
	Verified bool   `json:"verified"`
	Reason   string `json:"reason"`
}

// wireTree is the answer of the call that reads a tree.
type wireTree struct {
	SHA       string          `json:"sha"`
	Tree      []wireTreeEntry `json:"tree"`
	Truncated bool            `json:"truncated"`
}

type wireTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type wireApp struct {
	ID   json.Number `json:"id"`
	Slug string      `json:"slug"`
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

// wireRef is a branch in the answer of the calls that read or make a ref.
type wireRef struct {
	Ref    string        `json:"ref"`
	Object wireRefObject `json:"object"`
}

type wireRefObject struct {
	SHA  string `json:"sha"`
	Type string `json:"type"`
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

// wireValidation is the answer for a request that GitHub does not accept.
type wireValidation struct {
	Message string                `json:"message"`
	Errors  []wireValidationError `json:"errors"`
}

type wireValidationError struct {
	Resource string `json:"resource"`
	Field    string `json:"field,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message,omitempty"`
}

// wireBranch is the answer for one branch.
type wireBranch struct {
	Name      string     `json:"name"`
	Protected bool       `json:"protected"`
	Commit    wireParent `json:"commit"`
}
