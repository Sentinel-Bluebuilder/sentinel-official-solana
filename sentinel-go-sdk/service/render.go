package service

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/template"
)

func RenderBytes(fsys fs.FS, name string, data any) ([]byte, error) {
	funcs := template.FuncMap{
		"b64hex": b64hex,
		"join":   strings.Join,
	}

	tmpl, err := template.New(path.Base(name)).Funcs(funcs).ParseFS(fsys, name)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err //nolint:wrapcheck
	}

	return buf.Bytes(), nil
}

func WriteTemplate(fsys fs.FS, name, filePath string, data any, comment string) error {
	raw, err := RenderBytes(fsys, name, data)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", filePath, err)
	}

	return WriteOwned(filePath, raw, comment)
}

func b64hex(text string) (string, error) {
	raw, err := hex.DecodeString(text)
	if err != nil {
		return "", fmt.Errorf("decoding hex: %w", err)
	}

	return base64.StdEncoding.EncodeToString(raw), nil
}
