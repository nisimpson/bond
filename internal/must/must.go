package must

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
