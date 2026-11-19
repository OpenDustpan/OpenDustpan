package envfixture

import "os"

const shadowedName = "SHADOW_CONST_SECRET"

func shadow() string {
	shadowedName := "SHADOW_LOCAL_SECRET"

	return os.Getenv(shadowedName)
}
