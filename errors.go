package bond

import "errors"

// Requirement: CONV-5.1, CONV-5.2

// Sentinel error for context overflow
var ErrContextOverflow = errors.New("bond: context overflow")
