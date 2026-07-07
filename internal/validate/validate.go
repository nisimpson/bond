package validate

import (
	"errors"
	"fmt"
)

// ErrInvalid is the sentinel error returned when validation fails.
var ErrInvalid = errors.New("invalid")

// Rule represents a single validation rule that can be tested.
type Rule interface {
	test() error
}

// RuleFunc is an adapter to allow the use of ordinary functions as Rules.
type RuleFunc func() error

func (fn RuleFunc) test() error { return fn() }

// All evaluates every rule and returns a combined error wrapping ErrInvalid
// if any of them fail. All rules are always evaluated regardless of earlier failures.
func All(rules ...Rule) error {
	errs := make([]error, 0, len(rules))
	for _, rule := range rules {
		errs = append(errs, rule.test())
	}
	err := errors.Join(errs...)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// Any evaluates rules in order and returns an error wrapping ErrInvalid
// as soon as the first rule fails. Returns nil if all rules pass.
func Any(rules ...Rule) error {
	for _, rule := range rules {
		if err := rule.test(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return nil
}

// Group creates a Rule that evaluates the given rules using [Any] (short-circuit)
// and, if any rule fails, prefixes the resulting error with name for context.
// This is useful for grouping related validations under a descriptive label
// (e.g., a field name) so that error messages clearly indicate which group failed.
//
// Example usage:
//
//	validate.Group("email",
//	    validate.That(email != "", "is required"),
//	    validate.That(isValidEmail(email), "must be a valid email address"),
//	)
func Group(name string, rules ...Rule) Rule {
	return RuleFunc(func() error {
		if err := Any(rules...); err != nil {
			return fmt.Errorf("%s: %s", name, err)
		}
		return nil
	})
}

// That creates a RuleFunc from a boolean condition.
//
// The when parameter controls whether the rule passes or fails:
//   - If when is true, the rule passes (returns nil).
//   - If when is false, an error is constructed from the remaining arguments.
//
// The format parameter is a message string (or fmt-style format verb string)
// describing the validation failure. Any additional args are passed to
// [fmt.Errorf] as format parameters. If no args are provided, format is used
// directly as the error message via [errors.New].
//
// Example usage:
//
//	validate.That(age >= 18, "age must be at least 18, got %d", age)
//	validate.That(name != "", "name is required")
func That(when bool, format string, args ...any) RuleFunc {
	return func() error {
		if when {
			return nil
		}
		if len(args) == 0 {
			return errors.New(format)
		}
		return fmt.Errorf(format, args...)
	}
}
