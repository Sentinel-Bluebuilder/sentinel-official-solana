package cli

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type orderedField struct {
	name  string
	value any
}

type orderedObject []orderedField

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, field := range o {
		if i > 0 {
			buf.WriteByte(',')
		}

		name, err := json.Marshal(field.name)
		if err != nil {
			return nil, fmt.Errorf("encoding field name: %w", err)
		}

		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, fmt.Errorf("encoding field value: %w", err)
		}

		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}

func isJSONEmpty(value reflect.Value) bool {
	switch value.Kind() { //nolint:exhaustive
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return value.IsZero()
	default:
		return false
	}
}

func normalizeStruct(value reflect.Value) orderedObject {
	uriLength, hasLength, hasURI := 0, false, false

	for i := range value.NumField() {
		field := value.Type().Field(i)
		switch columnName(field) {
		case "uri_len":
			if field.IsExported() && value.Field(i).CanUint() {
				uriLength = int(value.Field(i).Uint())
				hasLength = true
			}
		case "uri":
			hasURI = field.Type.Kind() == reflect.Array && field.Type.Elem().Kind() == reflect.Uint8
		}
	}

	hasURI = hasURI && hasLength

	object := make(orderedObject, 0, value.NumField())
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if !field.IsExported() || field.Tag.Get("json") == "-" {
			continue
		}

		item := value.Field(i)
		if strings.Contains(field.Tag.Get("json"), ",omitempty") && isJSONEmpty(item) {
			continue
		}

		name := columnName(field)
		if name == "reserved" || (name == "uri_len" && hasURI) {
			continue
		}

		var result any

		switch {
		case name == "uri" && hasURI:
			length := min(uriLength, item.Len())

			raw := make([]byte, length)
			for j := range raw {
				raw[j] = byte(item.Index(j).Uint())
			}

			result = string(raw)
		case name == "country" && item.Kind() == reflect.Array && item.Len() == 2 &&
			item.Type().Elem().Kind() == reflect.Uint8:
			result = string([]byte{byte(item.Index(0).Uint()), byte(item.Index(1).Uint())})
		default:
			result = normalizeValue(item.Interface())
		}

		object = append(object, orderedField{
			name:  name,
			value: result,
		})
	}

	return object
}

func normalizeValue(value any) any {
	if value == nil {
		return nil
	}

	if _, ok := value.(json.Marshaler); ok {
		return value
	}

	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return nil
		}

		reflected = reflected.Elem()
	}

	switch reflected.Kind() { //nolint:exhaustive
	case reflect.Struct:
		return normalizeStruct(reflected)
	case reflect.Array, reflect.Slice:
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			return nil
		}

		if reflected.Kind() == reflect.Slice && reflected.Type().Elem().Kind() == reflect.Uint8 {
			return reflected.Interface()
		}

		if reflected.Kind() == reflect.Array && reflected.Type().Elem().Kind() == reflect.Uint8 {
			raw := make([]byte, reflected.Len())
			reflect.Copy(reflect.ValueOf(raw), reflected)

			return hex.EncodeToString(raw)
		}

		values := make([]any, reflected.Len())
		for i := range values {
			values[i] = normalizeValue(reflected.Index(i).Interface())
		}

		return values
	default:
		return reflected.Interface()
	}
}
