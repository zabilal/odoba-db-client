package source

import (
	"errors"
	"testing"
)

// The guard is the single chokepoint for NFR-S4. These tests are deliberately
// exhaustive over the (access x mode x environment) space, because a gap here
// is a silent write to production.

func TestGuardAllowsAllReads(t *testing.T) {
	for _, g := range []Guard{
		{},
		{ReadOnly: true},
		{Environment: EnvProduction},
		{ReadOnly: true, Environment: EnvProduction},
	} {
		if err := g.Allow(AccessRead, false); err != nil {
			t.Errorf("read refused by %#v: %v", g, err)
		}
	}
}

func TestGuardReadOnlyRefusesEveryMutation(t *testing.T) {
	g := Guard{ReadOnly: true, Environment: EnvLocal}

	for _, a := range []Access{AccessWrite, AccessDDL, AccessAdmin} {
		// Confirmation must not be a way around read-only mode.
		for _, confirmed := range []bool{false, true} {
			err := g.Allow(a, confirmed)
			if !errors.Is(err, ErrReadOnly) {
				t.Errorf("Allow(%v, confirmed=%v) = %v, want ErrReadOnly", a, confirmed, err)
			}
		}
	}
}

func TestGuardProductionRequiresConfirmation(t *testing.T) {
	g := Guard{Environment: EnvProduction}

	for _, a := range []Access{AccessWrite, AccessDDL, AccessAdmin} {
		if err := g.Allow(a, false); !errors.Is(err, ErrConfirmationRequired) {
			t.Errorf("unconfirmed %v = %v, want ErrConfirmationRequired", a, err)
		}
		if err := g.Allow(a, true); err != nil {
			t.Errorf("confirmed %v refused: %v", a, err)
		}
	}
}

func TestGuardNonProductionNeedsNoConfirmation(t *testing.T) {
	for _, env := range []Environment{EnvLocal, EnvDev, EnvStaging, ""} {
		g := Guard{Environment: env}
		if err := g.Allow(AccessWrite, false); err != nil {
			t.Errorf("write refused in %q: %v", env, err)
		}
	}
}

func TestRequiresConfirmationMatchesAllow(t *testing.T) {
	// The UI prompts ahead of the attempt based on RequiresConfirmation, so it
	// must agree with what Allow will actually do. A mismatch means either a
	// spurious prompt or an operation that fails after the user committed.
	envs := []Environment{EnvLocal, EnvDev, EnvStaging, EnvProduction}
	accesses := []Access{AccessRead, AccessWrite, AccessDDL, AccessAdmin}

	for _, ro := range []bool{false, true} {
		for _, env := range envs {
			for _, a := range accesses {
				g := Guard{ReadOnly: ro, Environment: env}
				wantPrompt := g.RequiresConfirmation(a)
				refusedUnconfirmed := errors.Is(g.Allow(a, false), ErrConfirmationRequired)

				if wantPrompt != refusedUnconfirmed {
					t.Errorf("ro=%v env=%q access=%v: RequiresConfirmation=%v but Allow refused=%v",
						ro, env, a, wantPrompt, refusedUnconfirmed)
				}
			}
		}
	}
}

func TestAccessMutating(t *testing.T) {
	if AccessRead.Mutating() {
		t.Error("read must not be mutating")
	}
	for _, a := range []Access{AccessWrite, AccessDDL, AccessAdmin} {
		if !a.Mutating() {
			t.Errorf("%v must be mutating", a)
		}
	}
}

func TestTLSVerifies(t *testing.T) {
	// Only the verifying modes may report that they verify — a bug here means
	// the UI shows a secure badge on an unverified connection.
	verifying := map[string]bool{
		"verify-full": true, "verify-ca": true,
		"require": false, "disable": false, "": false,
	}
	for mode, want := range verifying {
		if got := (TLSConfig{Mode: mode}).Verifies(); got != want {
			t.Errorf("mode %q: Verifies()=%v want %v", mode, got, want)
		}
	}
}
