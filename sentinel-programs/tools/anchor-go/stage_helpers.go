package main

import (
	"bytes"
	"fmt"
)

func renderHelpers(p *program) ([]byte, error) {
	var b bytes.Buffer

	fmt.Fprintf(&b, `package %s

import (
	%q
)

func newFieldError(field string, err error) error {
	return core.NewFieldError(field, err)
}

func newIndexError(index int, err error) error {
	return core.NewIndexError(index, err)
}

func newOptionError(field string, err error) error {
	return core.NewOptionError(field, err)
}
`, p.pkg, modulePath+"/core")

	return b.Bytes(), nil
}
