// Package e2e holds end-to-end tests of the user journeys in REQUIREMENTS.md:
// the real shell over real drivers, driven headlessly through the same
// widgets and commands a person uses. It lives outside internal/ui because
// depguard keeps drivers out of the UI packages, and a journey needs both.
//
// The journeys run on every engine. SQLite needs no server, so its journeys
// run in the ordinary test suite. PostgreSQL's need a server and carry the
// conformance build tag:
//
//	docker start ikigai-pg && go test -tags conformance ./internal/e2e/
package e2e
