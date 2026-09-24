package snapshot

import (
	"strings"
	"testing"
)

func FuzzParseRef(f *testing.F) {
	for _, seed := range []string{"s1/e1", " s42/e7\n", "s18446744073709551615/e1", "s0/e1", "s1/e0", "s1/e-1", "s1/e1suffix", "", "引用"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		snapshotID, elementID, err := ParseRef(input)
		if err != nil {
			return
		}
		if snapshotID == 0 || elementID <= 0 {
			t.Fatalf("accepted invalid identity: %d/%d", snapshotID, elementID)
		}
		ref := (Document{ID: snapshotID}).Ref(elementID)
		if ref != strings.TrimSpace(input) {
			t.Fatalf("accepted noncanonical reference %q as %q", input, ref)
		}
		gotSnapshot, gotElement, err := ParseRef(ref)
		if err != nil || gotSnapshot != snapshotID || gotElement != elementID {
			t.Fatalf("reference round trip failed: %q", ref)
		}
	})
}

func FuzzRenderCursor(f *testing.F) {
	for _, seed := range []string{"", "invalid", "e30", "bnVsbA", "eyJzIjoxLCJuIjowfQ", "eyJzIjoxLCJuIjoxfQ", "eyJzIjoyLCJuIjowfQ", "eyJzIjoxLCJuIjotMX0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, cursor string) {
		document := Document{ID: 1, Nodes: []Node{
			{ID: 1, Kind: KindSemantic, Role: "button", Name: "提交"},
			{Kind: KindText, Text: "Ready"},
			{ID: 2, Kind: KindCode, Text: "print(1)"},
		}}
		seen := make(map[string]bool)
		for page := 0; ; page++ {
			if seen[cursor] || page > len(document.Nodes) {
				t.Fatalf("pagination did not advance: %q", cursor)
			}
			seen[cursor] = true
			result, err := Render(document, RenderOptions{Cursor: cursor, MaxRunes: 16})
			if err != nil {
				if page != 0 {
					t.Fatalf("renderer produced an invalid continuation: %v", err)
				}
				return
			}
			if !result.HasMore {
				if result.Cursor != "" {
					t.Fatal("completed page retained a continuation")
				}
				return
			}
			if result.Cursor == "" || result.Markdown == "" {
				t.Fatal("continuation must include content and a cursor")
			}
			cursor = result.Cursor
		}
	})
}
