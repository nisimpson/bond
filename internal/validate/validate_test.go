package validate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nisimpson/bond/internal/validate"
)

func TestThat_PassesWhenTrue(t *testing.T) {
	rule := validate.That(true, "should not fail")
	if err := rule(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestThat_FailsWhenFalse(t *testing.T) {
	rule := validate.That(false, "name is required")
	err := rule()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "name is required" {
		t.Fatalf("expected 'name is required', got %q", err.Error())
	}
}

func TestThat_FailsWithFormatArgs(t *testing.T) {
	rule := validate.That(false, "age must be at least %d, got %d", 18, 15)
	err := rule()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expected := "age must be at least 18, got 15"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestAll_AllPass(t *testing.T) {
	err := validate.All(
		validate.That(true, "a"),
		validate.That(true, "b"),
		validate.That(true, "c"),
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAll_OneFails(t *testing.T) {
	err := validate.All(
		validate.That(true, "a"),
		validate.That(false, "b is required"),
		validate.That(true, "c"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, validate.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "b is required") {
		t.Fatalf("expected error to contain 'b is required', got %q", err.Error())
	}
}

func TestAll_MultipleFail(t *testing.T) {
	err := validate.All(
		validate.That(false, "first failure"),
		validate.That(false, "second failure"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, validate.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	// All evaluates every rule, so both errors should be present.
	if !strings.Contains(err.Error(), "first failure") {
		t.Fatalf("expected 'first failure' in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "second failure") {
		t.Fatalf("expected 'second failure' in error, got %q", err.Error())
	}
}

func TestAny_AllPass(t *testing.T) {
	err := validate.Any(
		validate.That(true, "a"),
		validate.That(true, "b"),
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAny_FirstFails(t *testing.T) {
	err := validate.Any(
		validate.That(false, "first check failed"),
		validate.That(true, "b"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, validate.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "first check failed") {
		t.Fatalf("expected 'first check failed' in error, got %q", err.Error())
	}
}

func TestAny_ShortCircuits(t *testing.T) {
	// If Any short-circuits, the second rule's error should not appear.
	err := validate.Any(
		validate.That(false, "stops here"),
		validate.That(false, "never evaluated"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stops here") {
		t.Fatalf("expected 'stops here' in error, got %q", err.Error())
	}
	// The second error should NOT be present since Any short-circuits.
	if strings.Contains(err.Error(), "never evaluated") {
		t.Fatalf("did not expect 'never evaluated' in error, got %q", err.Error())
	}
}

func TestGroup_AllPass(t *testing.T) {
	rule := validate.Group("email",
		validate.That(true, "is required"),
		validate.That(true, "must be valid"),
	)
	err := validate.All(rule)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestGroup_FirstFails(t *testing.T) {
	rule := validate.Group("email",
		validate.That(false, "is required"),
		validate.That(true, "must be valid"),
	)
	err := validate.All(rule)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "email") {
		t.Fatalf("expected 'email' prefix in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "is required") {
		t.Fatalf("expected 'is required' in error, got %q", err.Error())
	}
}

func TestAll_NoRules(t *testing.T) {
	err := validate.All()
	if err != nil {
		t.Fatalf("expected nil for empty rules, got %v", err)
	}
}

func TestAny_NoRules(t *testing.T) {
	err := validate.Any()
	if err != nil {
		t.Fatalf("expected nil for empty rules, got %v", err)
	}
}

func TestRuleFunc_Adapter(t *testing.T) {
	var called bool
	rule := validate.RuleFunc(func() error {
		called = true
		return nil
	})
	err := validate.All(rule)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !called {
		t.Fatal("expected RuleFunc to be called")
	}
}
