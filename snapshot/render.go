package snapshot

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RenderOptions controls snapshot Markdown pagination.
type RenderOptions struct {
	MaxRunes int
	Cursor   string
}

// Rendered is one Markdown page and its continuation cursor.
type Rendered struct {
	Markdown string `json:"markdown"`
	Cursor   string `json:"cursor,omitempty"`
	HasMore  bool   `json:"has_more"`
}

type pageCursor struct {
	SnapshotID uint64 `json:"s"`
	Node       int    `json:"n"`
}

// Render converts a structured snapshot document to Markdown.
func Render(document Document, options RenderOptions) (Rendered, error) {
	start := 0
	if options.Cursor != "" {
		cursor, err := decodeCursor(options.Cursor)
		if err != nil {
			return Rendered{}, err
		}
		if cursor.SnapshotID != document.ID {
			return Rendered{}, fmt.Errorf("stale snapshot cursor: s%d is not current s%d", cursor.SnapshotID, document.ID)
		}
		if cursor.Node < 0 || cursor.Node > len(document.Nodes) {
			return Rendered{}, fmt.Errorf("snapshot cursor node is out of range")
		}
		start = cursor.Node
	}

	var out strings.Builder
	if start == 0 && document.Title != "" {
		out.WriteString("# ")
		out.WriteString(escapeInline(document.Title))
		out.WriteString("\n\n")
	}
	limit := options.MaxRunes
	if limit <= 0 {
		limit = 32_000
	}
	used := utf8.RuneCountInString(out.String())
	end := start
	for ; end < len(document.Nodes); end++ {
		block := renderNode(document, document.Nodes[end])
		blockRunes := utf8.RuneCountInString(block)
		if used > 0 && used+blockRunes > limit {
			break
		}
		if blockRunes > limit && used == 0 {
			block = truncateRunes(block, limit)
			blockRunes = utf8.RuneCountInString(block)
		}
		out.WriteString(block)
		used += blockRunes
	}
	result := Rendered{Markdown: strings.TrimRight(out.String(), "\n")}
	if end < len(document.Nodes) {
		result.HasMore = true
		result.Cursor = encodeCursor(pageCursor{SnapshotID: document.ID, Node: end})
	}
	return result, nil
}

func renderNode(document Document, node Node) string {
	switch node.Kind {
	case KindText:
		return escapeText(node.Text) + "\n\n"
	case KindCode:
		fence := "```"
		for strings.Contains(node.Text, fence) {
			fence += "`"
		}
		label := ""
		if node.ID > 0 {
			label = fmt.Sprintf("%s- code [ref=%s]\n", strings.Repeat("  ", max(node.Depth, 0)), document.Ref(node.ID))
		}
		return label + fence + node.Language + "\n" + node.Text + "\n" + fence + "\n\n"
	case KindTable:
		label := fmt.Sprintf("%s- table", strings.Repeat("  ", max(node.Depth, 0)))
		if node.Name != "" {
			label += " " + quote(node.Name)
		}
		if node.ID > 0 {
			label += " [ref=" + document.Ref(node.ID) + "]"
		}
		return label + "\n" + renderTable(node.Rows)
	case KindFrame:
		label := first(node.Name, node.FrameURL, "frame")
		return fmt.Sprintf("%s- frame %s\n", strings.Repeat("  ", max(node.Depth, 0)), quote(label))
	default:
		role := first(node.Role, "element")
		var line strings.Builder
		line.WriteString(strings.Repeat("  ", max(node.Depth, 0)))
		line.WriteString("- ")
		line.WriteString(role)
		if node.Name != "" {
			line.WriteByte(' ')
			line.WriteString(quote(node.Name))
		}
		if node.Level > 0 {
			fmt.Fprintf(&line, " [level=%d]", node.Level)
		}
		if node.Value != "" {
			line.WriteString(" [value=")
			line.WriteString(quote(node.Value))
			line.WriteByte(']')
		}
		for _, state := range node.States {
			state = strings.TrimSpace(state)
			if state != "" {
				line.WriteString(" [")
				line.WriteString(escapeInline(state))
				line.WriteByte(']')
			}
		}
		if node.ID > 0 {
			line.WriteString(" [ref=")
			line.WriteString(document.Ref(node.ID))
			line.WriteByte(']')
		}
		line.WriteByte('\n')
		return line.String()
	}
}

func renderTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	if columns == 0 {
		return ""
	}
	var out strings.Builder
	writeRow := func(row []string) {
		out.WriteByte('|')
		for i := 0; i < columns; i++ {
			value := ""
			if i < len(row) {
				value = strings.ReplaceAll(escapeInline(row[i]), "|", "\\|")
			}
			out.WriteByte(' ')
			out.WriteString(value)
			out.WriteString(" |")
		}
		out.WriteByte('\n')
	}
	writeRow(rows[0])
	out.WriteByte('|')
	for range columns {
		out.WriteString(" --- |")
	}
	out.WriteByte('\n')
	for _, row := range rows[1:] {
		writeRow(row)
	}
	out.WriteByte('\n')
	return out.String()
}

func quote(value string) string {
	value = escapeInline(value)
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return `"` + value + `"`
}

func escapeInline(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return strings.NewReplacer("[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`").Replace(value)
}

func escapeText(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func encodeCursor(cursor pageCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value string) (pageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return pageCursor{}, fmt.Errorf("invalid snapshot cursor: %w", err)
	}
	var cursor pageCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return pageCursor{}, fmt.Errorf("invalid snapshot cursor: %w", err)
	}
	return cursor, nil
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
