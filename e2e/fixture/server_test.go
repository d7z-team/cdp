package fixture

import "testing"

func TestRouteForFixture(t *testing.T) {
	cases := map[string]string{
		"home.html":          "/",
		"eval.html":          "/eval",
		"iframe-nested.html": "/iframe-nested",
	}
	for name, want := range cases {
		if got := routeForFixture(name); got != want {
			t.Fatalf("routeForFixture(%q) = %q, want %q", name, got, want)
		}
	}
}
