package core

import (
	"strconv"
)

type fieldError struct {
	err   error
	field string
}

func NewFieldError(field string, err error) error {
	if err == nil {
		return nil
	}

	return &fieldError{
		field: field,
		err:   err,
	}
}

func (e *fieldError) Error() string {
	switch nested := e.err.(type) {
	case *fieldError, *optionError:
		return e.field + "." + nested.Error()
	case *indexError:
		return e.field + nested.Error()
	}

	return e.field + ": " + e.err.Error()
}

func (e *fieldError) Unwrap() error { return e.err }

type indexError struct {
	err   error
	index int
}

func NewIndexError(index int, err error) error {
	if err == nil {
		return nil
	}

	return &indexError{
		index: index,
		err:   err,
	}
}

func (e *indexError) Error() string {
	prefix := "[" + strconv.Itoa(e.index) + "]"

	switch nested := e.err.(type) {
	case *fieldError, *optionError:
		return prefix + "." + nested.Error()
	case *indexError:
		return prefix + nested.Error()
	}

	return prefix + ": " + e.err.Error()
}

func (e *indexError) Unwrap() error { return e.err }

type optionError struct {
	err   error
	field string
}

func NewOptionError(field string, err error) error {
	if err == nil {
		return nil
	}

	return &optionError{
		field: field,
		err:   err,
	}
}

func (e *optionError) Error() string {
	switch nested := e.err.(type) {
	case *fieldError, *optionError:
		return "?" + e.field + "." + nested.Error()
	case *indexError:
		return "?" + e.field + nested.Error()
	}

	return "?" + e.field + ": " + e.err.Error()
}

func (e *optionError) Unwrap() error { return e.err }
