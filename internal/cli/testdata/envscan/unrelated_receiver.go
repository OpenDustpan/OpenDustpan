package envfixture

type env struct{}

func (env) Getenv(string) string { return "" }

func unrelated() string { var e env; return e.Getenv("NOT_A_READ_SECRET") }
