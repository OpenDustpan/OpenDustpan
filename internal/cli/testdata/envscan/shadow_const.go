package envfixture

import "os"

const shadowedLocalConst = "SHADOW_FILE_CONST_SECRET"

func shadowConst() string {
	const shadowedLocalConst = "SHADOW_LOCAL_CONST_SECRET"

	return os.Getenv(shadowedLocalConst)
}
