package githubfake

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// The error types of a GraphQL answer.
const (
	gqlStale     = "STALE_DATA"
	gqlForbidden = "FORBIDDEN"
	gqlNotFound  = "NOT_FOUND"
	gqlBadInput  = "UNPROCESSABLE"
)

// gqlRequest is the body of a GraphQL call.
type gqlRequest struct {
	Query     string       `json:"query"`
	Variables gqlVariables `json:"variables"`
}

type gqlVariables struct {
	Input commitInput `json:"input"`
}

// commitInput is the input of the mutation createCommitOnBranch.
type commitInput struct {
	Branch          commitBranch  `json:"branch"`
	Message         commitMessage `json:"message"`
	FileChanges     fileChanges   `json:"fileChanges"`
	ExpectedHeadOid string        `json:"expectedHeadOid"`
}

type commitBranch struct {
	Repository string `json:"repositoryNameWithOwner"`
	Name       string `json:"branchName"`
}

type commitMessage struct {
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

type fileChanges struct {
	Additions []fileAddition `json:"additions"`
	Deletions []fileDeletion `json:"deletions"`
}

type fileAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

type fileDeletion struct {
	Path string `json:"path"`
}

// gqlError answers an error of a GraphQL call. GitHub answers 200 for these.
func (f *Fake) gqlError(w http.ResponseWriter, kind, text string) {
	f.writeJSON(w, http.StatusOK, wireGQLResponse{
		Data:   &wireGQLData{CreateCommitOnBranch: nil},
		Errors: []wireGQLError{{Type: kind, Message: text}},
	})
}

// graphql serves the mutation createCommitOnBranch. It makes one commit on
// an existing branch and fails when the head of the branch is not
// expectedHeadOid. A branch with protection refuses the commit.
func (f *Fake) graphql(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r, BearerInstallation, BearerUser) {
		return
	}

	var req gqlRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return
	}

	if !strings.Contains(req.Query, "createCommitOnBranch") {
		f.writeJSON(w, http.StatusOK, wireGQLResponse{
			Data: nil,
			Errors: []wireGQLError{
				{Type: gqlBadInput, Message: "the fake GitHub host serves only createCommitOnBranch"},
			},
		})

		return
	}

	in := req.Variables.Input

	rp, found := f.repos[in.Branch.Repository]
	if !found || !f.permitted(r, in.Branch.Repository, "metadata", levelNameRead) {
		f.gqlError(w, gqlNotFound, "Could not resolve to a Repository with the name '"+in.Branch.Repository+"'.")

		return
	}

	if !f.permitted(r, in.Branch.Repository, "contents", levelNameWrite) {
		f.gqlError(w, gqlForbidden, "Resource not accessible by integration")

		return
	}

	f.commitOnBranch(w, rp, in)
}

func (f *Fake) commitOnBranch(w http.ResponseWriter, rp *repo, in commitInput) {
	head, found := rp.branches[in.Branch.Name]

	switch {
	case !found:
		f.gqlError(w, gqlNotFound, "No such branch: "+in.Branch.Name)

		return
	case rp.protected[in.Branch.Name]:
		f.gqlError(w, gqlForbidden, "Repository rule violations found: protected branch "+in.Branch.Name)

		return
	case head != in.ExpectedHeadOid:
		f.gqlError(
			w,
			gqlStale,
			"Expected branch to point to \""+in.ExpectedHeadOid+"\" but it did not. Pull and try again.",
		)

		return
	}

	tip := rp.commits[head]
	if tip == nil {
		f.gqlError(w, gqlNotFound, "No such commit: "+head)

		return
	}

	// GitHub refuses the delete of a file that the branch does not have.
	for _, d := range in.FileChanges.Deletions {
		if _, has := tip.tree[d.Path]; !has {
			f.gqlError(w, gqlBadInput, "the file "+d.Path+" is not in the branch")

			return
		}
	}

	files := map[string][]byte{}

	for _, a := range in.FileChanges.Additions {
		content, err := base64.StdEncoding.DecodeString(a.Contents)
		if err != nil {
			f.gqlError(w, gqlBadInput, "the contents of "+a.Path+" are not base64")

			return
		}

		files[a.Path] = content
	}

	for _, d := range in.FileChanges.Deletions {
		files[d.Path] = nil
	}

	message := in.Message.Headline
	if in.Message.Body != "" {
		message += "\n\n" + in.Message.Body
	}

	c := rp.commit(head, files, message, f.now())
	rp.branches[in.Branch.Name] = c.meta.ID

	f.writeJSON(w, http.StatusOK, wireGQLResponse{
		Data: &wireGQLData{CreateCommitOnBranch: &wireGQLPayload{Commit: wireGQLCommit{
			OID: c.meta.ID, CommittedDate: c.meta.Time.UTC().Format("2006-01-02T15:04:05Z"),
		}}},
		Errors: nil,
	})
}
