package api

import "errors"

// specFieldError attaches the offending field of a saved specification to a
// validation error without changing its message. The field is a slash-separated
// path of JSON property names and array indexes (for example
// "models/1/surface_ref"), returned to clients as details.field.
type specFieldError struct {
	field string
	err   error
}

func (e *specFieldError) Error() string { return e.err.Error() }
func (e *specFieldError) Unwrap() error { return e.err }

// withField marks err as caused by field. A nil err stays nil, and the
// innermost (most specific) field wins when errors are wrapped twice.
func withField(field string, err error) error {
	if err == nil {
		return nil
	}
	var existing *specFieldError
	if errors.As(err, &existing) {
		return err
	}
	return &specFieldError{field: field, err: err}
}

// specErrorField returns the field a validation error names, if any.
func specErrorField(err error) (string, bool) {
	var fe *specFieldError
	if errors.As(err, &fe) {
		return fe.field, true
	}
	return "", false
}
