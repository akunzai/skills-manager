package engine

import (
	"errors"
	"fmt"
)

// refusalError is a Source this tool will not read: a well-known one not
// over https://, or an index or artifact it serves that cannot be verified
// or unpacked safely, or a Gist form such as a revision URL. It is for the user
// to resolve, not a failed operation (ADR-0002).
type refusalError struct{ err error }

func (e refusalError) Error() string { return e.err.Error() }
func (e refusalError) Unwrap() error { return e.err }

func refuse(format string, args ...any) error {
	return refusalError{fmt.Errorf(format, args...)}
}

// IsSourceRefusal reports whether err is a Source this tool will not read,
// as refusalError describes: a malformed Gist, or a well-known Source it
// cannot verify.
func IsSourceRefusal(err error) bool {
	_, ok := errors.AsType[refusalError](err)
	return ok
}
