package engine

import (
	"bytes"
	"encoding/json"
	"strings"
)

func jsStringLiteral(value string) string {
	return jsLiteral(value)
}

func jsLiteral(value any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(value)
	if err != nil {
		return `null`
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
