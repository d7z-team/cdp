package snapshot

import (
	"fmt"
	"reflect"
)

// Change describes one added, updated, or removed snapshot node.
type Change struct {
	Ref    string `json:"ref,omitempty"`
	Before *Node  `json:"before,omitempty"`
	After  *Node  `json:"after,omitempty"`
}

// Delta is the semantic difference between two snapshot documents.
type Delta struct {
	From    uint64   `json:"from"`
	To      uint64   `json:"to"`
	Added   []Change `json:"added,omitempty"`
	Changed []Change `json:"changed,omitempty"`
	Removed []Change `json:"removed,omitempty"`
}

// Diff computes the semantic changes from before to after.
func Diff(before, after Document) Delta {
	delta := Delta{From: before.ID, To: after.ID}
	old := make(map[string]Node, len(before.Nodes))
	for _, node := range before.Nodes {
		if key := nodeIdentity(node); key != "" {
			old[key] = node
		}
	}
	for _, node := range after.Nodes {
		key := nodeIdentity(node)
		if key == "" {
			continue
		}
		previous, ok := old[key]
		if !ok {
			n := node
			delta.Added = append(delta.Added, Change{Ref: after.Ref(node.ID), After: &n})
			continue
		}
		delete(old, key)
		if !semanticEqual(previous, node) {
			p, n := previous, node
			delta.Changed = append(delta.Changed, Change{Ref: after.Ref(node.ID), Before: &p, After: &n})
		}
	}
	for _, node := range before.Nodes {
		if _, ok := old[nodeIdentity(node)]; ok {
			n := node
			delta.Removed = append(delta.Removed, Change{Ref: before.Ref(node.ID), Before: &n})
		}
	}
	return delta
}

func semanticEqual(a, b Node) bool {
	a.ID, b.ID = 0, 0
	return reflect.DeepEqual(a, b)
}

func nodeIdentity(node Node) string {
	if node.Identity != "" {
		return "identity:" + node.Identity
	}
	if node.ID > 0 {
		return fmt.Sprintf("id:%d", node.ID)
	}
	return ""
}
