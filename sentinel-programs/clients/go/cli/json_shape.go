package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
)

var ErrArrayLengthMismatch = errors.New("array length mismatch")

type jsonField struct {
	index  []int
	name   string
	t      reflect.Type
	tagged bool
}

func checkJSONShape(raw []byte, t reflect.Type) error {
	if !json.Valid(raw) {
		var ignored any
		return json.Unmarshal(raw, &ignored)
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}

	return checkJSONValue(value, t)
}

func checkJSONValue(value any, t reflect.Type) error {
	if value == nil || t == nil {
		return nil
	}

	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	if t.Implements(unmarshaler) || reflect.PointerTo(t).Implements(unmarshaler) {
		return nil
	}

	switch t.Kind() {
	case reflect.Pointer:
		return checkJSONValue(value, t.Elem())
	case reflect.Array, reflect.Slice:
		elements, ok := value.([]any)
		if !ok {
			return nil
		}
		if t.Kind() == reflect.Array && len(elements) != t.Len() {
			return fmt.Errorf("%w: want %d, got %d", ErrArrayLengthMismatch, t.Len(), len(elements))
		}
		for i, element := range elements {
			if err := checkJSONValue(element, t.Elem()); err != nil {
				return core.NewIndexError(i, err)
			}
		}
	case reflect.Map:
		entries, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		for name, entry := range entries {
			if err := checkJSONValue(entry, t.Elem()); err != nil {
				return core.NewFieldError(name, err)
			}
		}
	case reflect.Struct:
		entries, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		for name, entry := range entries {
			if fieldType := findJSONFieldType(t, name); fieldType != nil {
				if err := checkJSONValue(entry, fieldType); err != nil {
					return core.NewFieldError(name, err)
				}
			}
		}
	}

	return nil
}

func collectJSONFields(t reflect.Type, index []int, seen map[reflect.Type]bool, fields *[]jsonField) {
	if seen[t] {
		return
	}
	seen[t] = true
	defer delete(seen, t)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if !field.IsExported() && (!field.Anonymous || fieldType.Kind() != reflect.Struct) {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		fieldIndex := append(slices.Clone(index), i)
		if field.Anonymous && name == "" && fieldType.Kind() == reflect.Struct {
			collectJSONFields(fieldType, fieldIndex, seen, fields)
			continue
		}
		if name == "" {
			name = field.Name
		}
		*fields = append(*fields, jsonField{
			index:  fieldIndex,
			name:   name,
			t:      field.Type,
			tagged: tag != "" && tag[0] != ',',
		})
	}
}

func findJSONFieldType(t reflect.Type, name string) reflect.Type {
	var fields []jsonField
	collectJSONFields(t, nil, make(map[reflect.Type]bool), &fields)
	slices.SortFunc(fields, func(a, b jsonField) int { return slices.Compare(a.index, b.index) })

	for pass := 0; pass < 2; pass++ {
		for _, field := range fields {
			if (pass == 0 && field.name != name) || (pass == 1 && !strings.EqualFold(field.name, name)) {
				continue
			}
			dominated := false
			for _, other := range fields {
				if other.name != field.name || slices.Equal(other.index, field.index) {
					continue
				}
				if len(other.index) < len(field.index) ||
					(len(other.index) == len(field.index) && (other.tagged || !field.tagged)) {
					dominated = true
					break
				}
			}
			if !dominated {
				return field.t
			}
		}
	}
	return nil
}
