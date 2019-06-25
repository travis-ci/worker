package errors

type JobAbortError interface {
	UserFacingErrorMessage() string
}

type wrappedJobAbortError struct {
	err     error
}

func NewWrappedJobAbortError(err error) error {
	return &wrappedJobAbortError{
		err: err,
	}
}

// we do not implement Cause(), because we want
// errors.Cause() to bottom out here

func (abortErr wrappedJobAbortError) Error() string {
	return abortErr.err.Error()
}

func (abortErr wrappedJobAbortError) UserFacingErrorMessage() string {
	return abortErr.err.Error()
}
