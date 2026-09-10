// Package editor implements the query editor — spike W2 (T0.45–T0.49) and the
// largest single build risk in the project (RISK-2).
//
// Tokenisation lives in internal/sqllex, which the source layer shares; this
// package owns the buffer, incremental highlighting, and (in Phase 1) the
// widget itself.
package editor
