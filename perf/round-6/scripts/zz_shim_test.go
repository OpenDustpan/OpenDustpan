package servertest_test

// workspaceAnswer is the JSON:API document that answers a created workspace (shim for the old tree).
type workspaceAnswer struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}
