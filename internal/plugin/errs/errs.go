package errs

import (
	"fmt"
)

type StatusError struct {
	status int
	err    error
}

func (e *StatusError) Error() string { return e.err.Error() }
func (e *StatusError) Unwrap() error { return e.err }

func WithStatus(status int, format string, args ...any) error {
	return &StatusError{status: status, err: fmt.Errorf(format, args...)}
}

func HTTPStatus(err error) int {
	var target *StatusError
	if err != nil && asStatusError(err, &target) {
		return target.status
	}
	return 500
}

func asStatusError(err error, target **StatusError) bool {
	for err != nil {
		if current, ok := err.(*StatusError); ok {
			*target = current
			return true
		}
		type unwrapper interface{ Unwrap() error }
		unwrapped, ok := err.(unwrapper)
		if !ok {
			break
		}
		err = unwrapped.Unwrap()
	}
	return false
}
