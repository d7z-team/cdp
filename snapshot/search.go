package snapshot

import "strings"

// SearchQuery filters snapshot nodes by text, role, name, and states.
type SearchQuery struct {
	Text   string   `json:"text,omitempty"`
	Role   string   `json:"role,omitempty"`
	Name   string   `json:"name,omitempty"`
	States []string `json:"states,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

// Match is one snapshot search result.
type Match struct {
	Ref  string `json:"ref,omitempty"`
	Node Node   `json:"node"`
}

// Search returns snapshot nodes matching query in document order.
func Search(document Document, query SearchQuery) []Match {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	text := strings.ToLower(strings.TrimSpace(query.Text))
	role := strings.ToLower(strings.TrimSpace(query.Role))
	name := strings.ToLower(strings.TrimSpace(query.Name))
	wantedStates := make([]string, 0, len(query.States))
	for _, state := range query.States {
		if state = strings.ToLower(strings.TrimSpace(state)); state != "" {
			wantedStates = append(wantedStates, state)
		}
	}
	matches := make([]Match, 0, min(limit, len(document.Nodes)))
	for _, node := range document.Nodes {
		if role != "" && strings.ToLower(node.Role) != role {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(node.Name), name) {
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{node.Name, node.Text, node.Value}, " "))
		if text != "" && !strings.Contains(haystack, text) {
			continue
		}
		states := strings.ToLower(strings.Join(node.States, "\n"))
		matched := true
		for _, state := range wantedStates {
			if !strings.Contains(states, state) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		match := Match{Node: node}
		if node.ID > 0 && node.Kind != KindText {
			match.Ref = document.Ref(node.ID)
		}
		matches = append(matches, match)
		if len(matches) == limit {
			break
		}
	}
	return matches
}
