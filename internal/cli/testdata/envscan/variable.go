package envfixture

import "os"

func variable() (string, bool) {
	look := os.LookupEnv

	return look("HIDDEN_SECRET")
}
