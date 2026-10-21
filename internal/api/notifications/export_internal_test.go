package notifications

// ClassifyText returns the text that the API answers for err, so the external
// tests reach the error mapping without a request.
func ClassifyText(err error) string { return classify(err).message }
