package must

import "encoding/json"

// Return takes a value and an error, returning the value if err is nil.
// If err is non-nil, it panics with the error. This is useful for wrapping
// calls that return (T, error) in contexts where errors are unexpected and
// should be treated as fatal.
func Return[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// JSON marshals v to JSON bytes. If marshaling fails, it returns the bytes
// for "null" rather than panicking, making it safe for use in logging and
// other best-effort serialization contexts.
func JSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return data
}
