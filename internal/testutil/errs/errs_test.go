package errs

import (
	"errors"
	"fmt"
	"testing"
)

// The distinction the package exists for: a wrapper matches, and is not the
// same error. A test that means "this error, not one carrying it" needs the
// second answer, which errors.Is does not give.
func TestAWrapperMatchesAndIsNotTheSameError(t *testing.T) {
	mine := errors.New("the server refused the connection")
	around := fmt.Errorf("dialling: %w", mine)

	if !Same(mine, mine) {
		t.Error("an error is not itself")
	}
	if Same(mine, around) {
		t.Error("an error and something carrying it read as one value")
	}
	if !errors.Is(around, mine) {
		t.Error("the wrapper does not match what it carries, so this proves nothing")
	}

	// Two errors built the same way are two errors, which is what makes the
	// driver assertions worth making.
	if Same(errors.New("no"), errors.New("no")) {
		t.Error("two errors with one message read as one value")
	}
	if !Same(nil, nil) {
		t.Error("nothing is not nothing")
	}
	if Same(nil, mine) || Same(mine, nil) {
		t.Error("an error and nothing read as one value")
	}
}
