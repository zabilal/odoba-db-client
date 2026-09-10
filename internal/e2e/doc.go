// Package e2e holds end-to-end tests of the user journeys in REQUIREMENTS.md:
// the real shell over the real drivers, driven headlessly. It lives outside
// internal/ui because depguard keeps drivers out of the UI packages, and a
// journey needs both.
//
// The tests need a database server and carry the conformance build tag:
//
//	docker start ikigai-pg && go test -tags conformance ./internal/e2e/
package e2e
