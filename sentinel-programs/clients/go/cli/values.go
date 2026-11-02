package cli

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
)

type fixedBytesValue struct {
	p []byte
}

func (v *fixedBytesValue) Set(s string) error {
	return parseFixedBytes(v.p, s)
}

func (v *fixedBytesValue) String() string {
	for _, b := range v.p {
		if b != 0 {
			return hex.EncodeToString(v.p)
		}
	}

	return ""
}

func (v *fixedBytesValue) Type() string {
	return "bytes"
}

type optional[T any] struct {
	p     **T
	parse func(string) (T, error)
	typ   string
}

func (v *optional[T]) Set(s string) error {
	x, err := v.parse(s)
	if err != nil {
		return err
	}

	*v.p = &x

	return nil
}

func (v *optional[T]) String() string {
	if *v.p == nil {
		return ""
	}

	return fmt.Sprint(**v.p)
}

func (v *optional[T]) Type() string {
	return v.typ
}

type slice[T any] struct {
	p     *[]T
	parse func(string) (T, error)
	typ   string
}

func (v *slice[T]) Set(s string) error {
	parts := strings.Split(s, ",")
	values := make([]T, len(parts))
	for i, part := range parts {
		x, err := v.parse(part)
		if err != nil {
			return err
		}

		values[i] = x
	}

	*v.p = append(*v.p, values...)

	return nil
}

func (v *slice[T]) String() string {
	if len(*v.p) == 0 {
		return ""
	}

	return fmt.Sprint(*v.p)
}

func (v *slice[T]) Type() string {
	return v.typ
}

type value[T any] struct {
	p     *T
	parse func(string) (T, error)
	typ   string
}

func (v *value[T]) Set(s string) error {
	x, err := v.parse(s)
	if err != nil {
		return err
	}

	*v.p = x

	return nil
}

func (v *value[T]) String() string {
	if reflect.ValueOf(*v.p).IsZero() {
		return ""
	}

	return fmt.Sprint(*v.p)
}

func (v *value[T]) Type() string {
	return v.typ
}

func AnyChangedWithPrefix(fs *pflag.FlagSet, prefix string) bool {
	changed := false

	fs.Visit(func(f *pflag.Flag) {
		if strings.HasPrefix(f.Name, prefix) {
			changed = true
		}
	})

	return changed
}

func FixedBytesVar(fs *pflag.FlagSet, p []byte, name, usage string) {
	fs.Var(&fixedBytesValue{p: p}, name, usage)
}

func OptionalVar[T any](fs *pflag.FlagSet, p **T, name, usage, typ string, parse func(string) (T, error)) {
	fs.Var(&optional[T]{
		p:     p,
		parse: parse,
		typ:   typ,
	}, name, usage)
}

func SliceVar[T any](fs *pflag.FlagSet, p *[]T, name, usage, typ string, parse func(string) (T, error)) {
	fs.Var(&slice[T]{
		p:     p,
		parse: parse,
		typ:   typ,
	}, name, usage)
}

func Var[T any](fs *pflag.FlagSet, p *T, name, usage, typ string, parse func(string) (T, error)) {
	fs.Var(&value[T]{
		p:     p,
		parse: parse,
		typ:   typ,
	}, name, usage)
}

func parseFixedBytes(dst []byte, s string) error {
	n := len(dst)

	switch len(s) {
	case n:
		copy(dst, s)

		return nil
	case 2 * n:
		b, err := hex.DecodeString(s)
		if err != nil {
			return err
		}

		copy(dst, b)

		return nil
	}

	return fmt.Errorf("expected %d characters or %d hexadecimal characters, got %d", n, 2*n, len(s))
}
