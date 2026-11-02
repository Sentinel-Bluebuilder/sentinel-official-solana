package cli

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
)

type EnumDecoder struct {
	typeOf   reflect.Type
	variants map[string]func() reflect.Value
}

func NewEnumDecoder[T any](variants map[string]func() T) EnumDecoder {
	decoder := EnumDecoder{
		typeOf:   reflect.TypeFor[T](),
		variants: make(map[string]func() reflect.Value, len(variants)),
	}
	for name, newVariant := range variants {
		decoder.variants[name] = func() reflect.Value { return reflect.ValueOf(newVariant()) }
	}

	return decoder
}

func ParseJSONWithEnums[T any](enums ...EnumDecoder) func(string) (T, error) {
	registry := make(map[reflect.Type]EnumDecoder, len(enums))
	for _, enum := range enums {
		registry[enum.typeOf] = enum
	}

	return func(s string) (T, error) {
		var value T
		data, err := ParseJSON[json.RawMessage](s)
		if err != nil {
			return value, err
		}

		err = decodeJSONEnums(data, reflect.ValueOf(&value).Elem(), registry)

		return value, err
	}
}

func containsJSONEnum(t reflect.Type, registry map[reflect.Type]EnumDecoder, seen map[reflect.Type]bool) bool {
	if _, ok := registry[t]; ok {
		return true
	}

	if seen[t] {
		return false
	}

	seen[t] = true
	for _, custom := range []reflect.Type{reflect.TypeFor[json.Unmarshaler](), reflect.TypeFor[encoding.TextUnmarshaler]()} {
		if t.Implements(custom) || reflect.PointerTo(t).Implements(custom) {
			return false
		}
	}

	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return containsJSONEnum(t.Elem(), registry, seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.IsExported() && field.Tag.Get("json") != "-" && containsJSONEnum(field.Type, registry, seen) {
				return true
			}
		}
	}

	return false
}

func decodeJSONEnums(data []byte, value reflect.Value, registry map[reflect.Type]EnumDecoder) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) || !containsJSONEnum(value.Type(), registry, make(map[reflect.Type]bool)) {
		return json.Unmarshal(data, value.Addr().Interface())
	}

	if enum, ok := registry[value.Type()]; ok {
		var tagged map[string]json.RawMessage
		if err := json.Unmarshal(data, &tagged); err != nil {
			return err
		}

		if len(tagged) != 1 {
			return fmt.Errorf("%s: expected one JSON variant tag", value.Type())
		}

		for tag, payload := range tagged {
			newVariant, ok := enum.variants[tag]
			if !ok {
				return fmt.Errorf("%s: unknown JSON variant %q", value.Type(), tag)
			}

			variant := newVariant()
			if variant.Kind() != reflect.Pointer || variant.IsNil() || !variant.Type().AssignableTo(value.Type()) {
				return fmt.Errorf("%s: invalid constructor for JSON variant %q", value.Type(), tag)
			}

			if bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
				return fmt.Errorf("%s: JSON variant %q requires a value", value.Type(), tag)
			}

			if err := decodeJSONEnums(payload, variant.Elem(), registry); err != nil {
				return fmt.Errorf("%s.%s: %w", value.Type(), tag, err)
			}

			value.Set(variant)
		}

		return nil
	}

	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			value.Set(reflect.New(value.Type().Elem()))
		}

		return decodeJSONEnums(data, value.Elem(), registry)
	case reflect.Slice, reflect.Array:
		var elements []json.RawMessage
		if err := json.Unmarshal(data, &elements); err != nil {
			return err
		}

		if value.Kind() == reflect.Slice {
			value.Set(reflect.MakeSlice(value.Type(), len(elements), len(elements)))
		}

		for i := 0; i < value.Len(); i++ {
			if i >= len(elements) {
				value.Index(i).SetZero()
				continue
			}

			if err := decodeJSONEnums(elements[i], value.Index(i), registry); err != nil {
				return fmt.Errorf("index %d: %w", i, err)
			}
		}

		return nil
	case reflect.Struct:
		fields := make([]reflect.StructField, value.NumField())
		nested := make([]bool, len(fields))
		for i := range fields {
			fields[i] = value.Type().Field(i)
			nested[i] = fields[i].IsExported() && fields[i].Tag.Get("json") != "-" && containsJSONEnum(fields[i].Type, registry, make(map[reflect.Type]bool))
			if nested[i] {
				fields[i].Type = reflect.TypeFor[json.RawMessage]()
				fields[i].Anonymous = false
			}
		}

		shadow := reflect.New(reflect.StructOf(fields)).Elem()
		if err := json.Unmarshal(data, shadow.Addr().Interface()); err != nil {
			return err
		}

		for i := range fields {
			if !value.Field(i).CanSet() {
				continue
			}

			if !nested[i] {
				value.Field(i).Set(shadow.Field(i))
				continue
			}

			if raw := shadow.Field(i).Bytes(); len(raw) != 0 {
				if err := decodeJSONEnums(raw, value.Field(i), registry); err != nil {
					return fmt.Errorf("%s.%s: %w", value.Type(), fields[i].Name, err)
				}
			}
		}

		return nil
	}

	return json.Unmarshal(data, value.Addr().Interface())
}
