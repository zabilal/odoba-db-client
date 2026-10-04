// Package errs holds the one error comparison that tests need and errors.Is
// cannot express: identity.
//
// Every driver classifies the failures it gets from its server into
// source.ConnectError, and every driver is tested on the two cases where it
// must not: a failure already said in those terms, and one it does not
// recognise, both of which it hands back as it was given. That assertion is
// about the value, not about what the value matches: errors.Is accepts a
// wrapper, so a classifier that wrapped its input instead of returning it
// would pass an errors.Is check while breaking the guarantee. Comparison says
// what is meant, and this is where it is said once rather than in each driver
// with a linter exception beside it.
package errs

// Same reports whether two errors are the one value.
//
// Use it where a function must return an error it was given rather than one
// that merely matches it; use errors.Is everywhere else.
func Same(a, b error) bool {
	return a == b //nolint:errorlint // identity is the assertion; see the package comment
}
