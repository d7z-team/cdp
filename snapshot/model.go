// Package snapshot models and renders page snapshots for AI consumption.
package snapshot

import (
	"fmt"
	"strings"
)

// Kind identifies the rendered role of a snapshot node.
type Kind string

const (
	// KindSemantic represents an interactive or semantic element.
	KindSemantic Kind = "semantic"
	// KindText represents plain document text.
	KindText Kind = "text"
	// KindCode represents a code block.
	KindCode Kind = "code"
	// KindTable represents tabular content.
	KindTable Kind = "table"
	// KindFrame represents an iframe boundary.
	KindFrame Kind = "frame"
)

// Document is a versioned, structured snapshot of a rendered page.
type Document struct {
	ID       uint64   `json:"id"`
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Lang     string   `json:"lang,omitempty"`
	Revision int64    `json:"revision"`
	Nodes    []Node   `json:"nodes"`
	Warnings []string `json:"warnings,omitempty"`
}

// Node is one addressable item in a snapshot document.
type Node struct {
	ID        int        `json:"id"`
	Kind      Kind       `json:"kind"`
	Depth     int        `json:"depth,omitempty"`
	Role      string     `json:"role,omitempty"`
	Name      string     `json:"name,omitempty"`
	Text      string     `json:"text,omitempty"`
	Value     string     `json:"value,omitempty"`
	Language  string     `json:"language,omitempty"`
	Level     int        `json:"level,omitempty"`
	States    []string   `json:"states,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	RuntimeID string     `json:"runtime_id,omitempty"`
	FrameURL  string     `json:"frame_url,omitempty"`
	Identity  string     `json:"identity,omitempty"`
}

// Ref returns the external reference for elementID in this document.
func (d Document) Ref(elementID int) string {
	return fmt.Sprintf("s%d/e%d", d.ID, elementID)
}

// ParseRef parses a snapshot element reference.
func ParseRef(value string) (snapshotID uint64, elementID int, err error) {
	value = strings.TrimSpace(value)
	if _, err = fmt.Sscanf(value, "s%d/e%d", &snapshotID, &elementID); err != nil || snapshotID == 0 || elementID <= 0 {
		return 0, 0, fmt.Errorf("invalid snapshot ref %q", value)
	}
	if value != fmt.Sprintf("s%d/e%d", snapshotID, elementID) {
		return 0, 0, fmt.Errorf("invalid snapshot ref %q", value)
	}
	return snapshotID, elementID, nil
}

// NodeByID returns the node with id.
func (d Document) NodeByID(id int) (Node, bool) {
	for _, node := range d.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return Node{}, false
}
