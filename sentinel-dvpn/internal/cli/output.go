package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jedib0t/go-pretty/v6/table"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	FormatJSON  = "json"
	FormatTable = "table"
)

var errRecordTypesMismatch = errors.New("mixed record types")

type Printer struct {
	format string
	writer io.Writer
}

func NewPrinter(writer io.Writer, format string) *Printer {
	return &Printer{
		format: format,
		writer: writer,
	}
}

func (p *Printer) Print(value any) error {
	if msg, ok := value.(proto.Message); ok {
		if p.format == FormatTable {
			return writeTable(p.writer, protoObject(msg.ProtoReflect()))
		}

		encoded, err := encodeProto(msg)
		if err != nil {
			return err
		}

		return writeJSON(p.writer, json.RawMessage(encoded))
	}

	value = normalizeValue(value)
	if p.format == FormatTable {
		return writeTable(p.writer, value)
	}

	return writeJSON(p.writer, value)
}

func appendObjectRows(grid table.Writer, object orderedObject) bool {
	grid.AppendHeader(table.Row{"field", "value"})

	fields := flattenObject(object, "")
	for _, field := range fields {
		grid.AppendRow(table.Row{escapeControl(field.name), escapeControl(fmt.Sprint(field.value))})
	}

	return len(fields) > 0
}

func appendRecordRows(grid table.Writer, columns []string, records []map[string]any) bool {
	header := make(table.Row, 0, len(columns))
	for _, name := range columns {
		header = append(header, escapeControl(name))
	}

	grid.AppendHeader(header)

	for _, record := range records {
		row := table.Row{}

		for _, name := range columns {
			if field, ok := record[name]; ok {
				row = append(row, escapeControl(fmt.Sprint(field)))
			} else {
				row = append(row, "")
			}
		}

		grid.AppendRow(row)
	}

	return len(records) > 0
}

func collectRecords(items []any) ([]string, []map[string]any, error) {
	columns := []string{}
	seen := make(map[string]bool)
	records := make([]map[string]any, len(items))

	for i, item := range items {
		object, ok := item.(orderedObject)
		if !ok {
			return nil, nil, fmt.Errorf("writing table: %w", errRecordTypesMismatch)
		}

		record := make(map[string]any)
		for _, field := range flattenObject(object, "") {
			record[field.name] = field.value
			if !seen[field.name] {
				columns = append(columns, field.name)
				seen[field.name] = true
			}
		}

		records[i] = record
	}

	return columns, records, nil
}

func columnName(field reflect.StructField) string {
	tag, ok := field.Tag.Lookup("json")
	if !ok || tag == "" || tag == "-" {
		return field.Name
	}

	name, _, _ := strings.Cut(tag, ",")

	return name
}

func encodeProto(msg proto.Message) ([]byte, error) {
	encoded, err := json.Marshal(protoObject(msg.ProtoReflect()))
	if err != nil {
		return nil, fmt.Errorf("encoding proto: %w", err)
	}

	return encoded, nil
}

func escapeControl(text string) string {
	for i, r := range text {
		if _, size := utf8.DecodeRuneInString(text[i:]); !strconv.IsPrint(r) || (r == utf8.RuneError && size == 1) {
			quoted := strconv.Quote(text)

			return quoted[1 : len(quoted)-1]
		}
	}

	return text
}

func flattenObject(object orderedObject, prefix string) orderedObject {
	fields := orderedObject{}

	for _, field := range object {
		name := field.name
		if prefix != "" {
			name = prefix + "." + name
		}

		if nested, ok := field.value.(orderedObject); ok {
			fields = append(fields, flattenObject(nested, name)...)

			continue
		}

		fields = append(fields, orderedField{
			name:  name,
			value: field.value,
		})
	}

	return fields
}

func protoObject(msg protoreflect.Message) orderedObject {
	fields := msg.Descriptor().Fields()
	object := make(orderedObject, 0, fields.Len())

	for i := range fields.Len() {
		field := fields.Get(i)

		var value any

		switch {
		case field.IsList():
			list := msg.Get(field).List()

			items := make([]any, list.Len())
			for j := range list.Len() {
				items[j] = protoValue(field, list.Get(j))
			}

			value = items
		case field.IsMap():
			entries := make(map[string]any)

			msg.Get(field).Map().Range(func(key protoreflect.MapKey, item protoreflect.Value) bool {
				entries[fmt.Sprint(key.Interface())] = protoValue(field.MapValue(), item)

				return true
			})

			value = entries
		case field.Kind() == protoreflect.MessageKind && !msg.Has(field):
			value = nil
		default:
			value = protoValue(field, msg.Get(field))
		}

		object = append(object, orderedField{
			name:  string(field.Name()),
			value: value,
		})
	}

	return object
}

func protoValue(field protoreflect.FieldDescriptor, value protoreflect.Value) any {
	switch field.Kind() { //nolint:exhaustive
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return protoObject(value.Message())
	case protoreflect.EnumKind:
		if item := field.Enum().Values().ByNumber(value.Enum()); item != nil {
			return string(item.Name())
		}

		return value.Enum()
	case protoreflect.BytesKind:
		return append([]byte{}, value.Bytes()...)
	default:
		return value.Interface()
	}
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encoding json: %w", err)
	}

	return nil
}

func writeTable(writer io.Writer, value any) error {
	grid := table.NewWriter()
	grid.SetOutputMirror(writer)

	render := false

	switch value := value.(type) {
	case []any:
		if len(value) == 0 {
			if _, err := fmt.Fprintln(writer, "(empty)"); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}

		if _, ok := value[0].(orderedObject); !ok {
			break
		}

		columns, records, err := collectRecords(value)
		if err != nil {
			return err
		}

		render = appendRecordRows(grid, columns, records)
	case orderedObject:
		render = appendObjectRows(grid, value)
	}

	if !render {
		if _, err := fmt.Fprintln(writer, value); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}

		return nil
	}

	grid.Render()

	return nil
}
