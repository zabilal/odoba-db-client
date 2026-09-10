// Package postgres is the PostgreSQL driver — TASKS.md T1.32–T1.34, and the
// reference implementation of the source contract.
//
// It is the first real driver, so it is also the first real test of the
// contract designed in Phase 0 (ADR-0005). Everything here is written against
// the interfaces in internal/source; anything that proves awkward is a finding
// about the contract, to be fixed there rather than worked around here.
//
// This package must not import any UI package (ARCH-1).
package postgres
