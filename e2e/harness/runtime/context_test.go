package runtime

import "testing"

func TestNormalizeFixturePath(t *testing.T) {
	cases := map[string]string{
		"":      "/",
		"/":     "/",
		"form":  "/form",
		"/eval": "/eval",
	}
	for input, want := range cases {
		if got := normalizeFixturePath(input); got != want {
			t.Fatalf("normalizeFixturePath(%q) = %q, want %q", input, got, want)
		}
	}
}
