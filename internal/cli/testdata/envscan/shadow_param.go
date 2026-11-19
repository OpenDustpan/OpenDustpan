package envfixture

import "os"

const shadowedParam = "SHADOW_PARAM_CONST_SECRET"

func shadowParam(shadowedParam string) string { return os.Getenv(shadowedParam) }
