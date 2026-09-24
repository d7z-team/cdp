package snapshot

import (
	"strings"
	"testing"
)

func TestRenderSemanticMarkdown(t *testing.T) {
	doc := Document{ID: 17, Title: "Checkout [draft]", Nodes: []Node{
		{ID: 2, Role: "heading", Name: "Checkout", Level: 1},
		{ID: 3, Role: "textbox", Name: "Email", Value: "用户@example.com"},
		{ID: 4, Role: "checkbox", Name: "Remember me", States: []string{"checked"}},
	}}
	got, err := Render(doc, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`# Checkout \[draft\]`, `- heading "Checkout" [level=1] [ref=s17/e2]`, `[value="用户@example.com"]`, `[checked] [ref=s17/e4]`} {
		if !strings.Contains(got.Markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got.Markdown)
		}
	}
}

func TestRenderTableCodeAndUnicodeCursor(t *testing.T) {
	doc := Document{ID: 9, Nodes: []Node{
		{Kind: KindTable, Rows: [][]string{{"Name", "A|B"}, {"中文", "ok"}}},
		{Kind: KindCode, Language: "go", Text: "fmt.Println(```)"},
		{Kind: KindText, Text: strings.Repeat("界", 20)},
	}}
	first, err := Render(doc, RenderOptions{MaxRunes: 45})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.Cursor == "" || !strings.Contains(first.Markdown, `A\|B`) {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := Render(doc, RenderOptions{MaxRunes: 45, Cursor: first.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.Markdown, "�") || !strings.Contains(second.Markdown, "````go") {
		t.Fatalf("unexpected second page: %+v", second)
	}
	doc.ID++
	if _, err := Render(doc, RenderOptions{Cursor: first.Cursor}); err == nil {
		t.Fatal("expected stale cursor error")
	}
}

func TestSearchAndDiff(t *testing.T) {
	before := Document{ID: 1, Nodes: []Node{{ID: 1, Role: "button", Name: "Save"}, {ID: 2, Role: "checkbox", Name: "Keep", States: []string{"checked"}}}}
	matches := Search(before, SearchQuery{Role: "checkbox", States: []string{"checked"}})
	if len(matches) != 1 || matches[0].Ref != "s1/e2" {
		t.Fatalf("matches = %+v", matches)
	}
	after := Document{ID: 2, Nodes: []Node{{ID: 1, Role: "button", Name: "Saved"}, {ID: 3, Role: "link", Name: "Next"}}}
	delta := Diff(before, after)
	if len(delta.Changed) != 1 || len(delta.Added) != 1 || len(delta.Removed) != 1 {
		t.Fatalf("delta = %+v", delta)
	}
}

func TestDiffUsesStableIdentityAndIncludesLeafTextChanges(t *testing.T) {
	before := Document{ID: 1, Nodes: []Node{
		{ID: 1, Kind: KindText, Text: "Loading", Identity: "top/1"},
		{ID: 2, Kind: KindSemantic, Role: "button", Name: "Save", Identity: "top/2"},
	}}
	after := Document{ID: 2, Nodes: []Node{
		{ID: 1, Kind: KindSemantic, Role: "heading", Name: "Status", Identity: "top/3"},
		{ID: 2, Kind: KindText, Text: "Loaded", Identity: "top/1"},
		{ID: 3, Kind: KindSemantic, Role: "button", Name: "Save", Identity: "top/2"},
	}}
	delta := Diff(before, after)
	if len(delta.Added) != 1 || delta.Added[0].After.Identity != "top/3" {
		t.Fatalf("added = %+v", delta.Added)
	}
	if len(delta.Changed) != 1 || delta.Changed[0].Before.Text != "Loading" || delta.Changed[0].After.Text != "Loaded" {
		t.Fatalf("changed = %+v", delta.Changed)
	}
	if len(delta.Removed) != 0 {
		t.Fatalf("removed = %+v", delta.Removed)
	}
	matches := Search(after, SearchQuery{Text: "Loaded"})
	if len(matches) != 1 || matches[0].Ref != "" {
		t.Fatalf("text matches = %+v", matches)
	}
}

func TestParseRefRejectsTrailingData(t *testing.T) {
	if sid, eid, err := ParseRef("s17/e5"); err != nil || sid != 17 || eid != 5 {
		t.Fatalf("parse = %d/%d/%v", sid, eid, err)
	}
	for _, value := range []string{"", "s0/e1", "s1/e0", "s1/e2/x", "1/2"} {
		if _, _, err := ParseRef(value); err == nil {
			t.Fatalf("ParseRef(%q) succeeded", value)
		}
	}
}
