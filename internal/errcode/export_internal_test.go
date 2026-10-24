package errcode

// SpecAnswer gives the tests the answer of a spec that is not in the catalog,
// to prove the checks of New on every kind and limit.
func SpecAnswer(s Spec, values ...Value) (Answer, error) { return s.answer(values...) }
