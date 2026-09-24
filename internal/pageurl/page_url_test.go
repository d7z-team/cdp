package pageurl

import "testing"

func TestClassifyPageURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want PageURLKind
	}{
		{name: "empty", url: "", want: PageURLPlaceholder},
		{name: "blank", url: "about:blank", want: PageURLPlaceholder},
		{name: "chrome newtab", url: "chrome://newtab/", want: PageURLPlaceholder},
		{name: "chrome new tab page", url: "chrome://new-tab-page/", want: PageURLPlaceholder},
		{name: "chrome search newtab", url: "chrome-search://local-ntp/local-ntp.html", want: PageURLPlaceholder},
		{name: "edge newtab", url: "edge://newtab/", want: PageURLPlaceholder},
		{name: "edge new tab page", url: "edge://new-tab-page/", want: PageURLPlaceholder},
		{name: "edge search newtab", url: "edge-search://local-ntp/local-ntp.html", want: PageURLPlaceholder},
		{name: "devtools", url: "devtools://devtools/bundled/inspector.html", want: PageURLInternal},
		{name: "chrome settings", url: "chrome://settings/", want: PageURLInternal},
		{name: "extension", url: "chrome-extension://abc/index.html", want: PageURLInternal},
		{name: "javascript", url: "javascript:alert(1)", want: PageURLInternal},
		{name: "https", url: "https://example.com", want: PageURLExecutable},
		{name: "file", url: "file:///tmp/index.html", want: PageURLExecutable},
		{name: "custom scheme", url: "myapp://host/path", want: PageURLExecutable},
		{name: "invalid parse fallback", url: "http://%zz", want: PageURLExecutable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyPageURL(tt.url); got != tt.want {
				t.Fatalf("ClassifyPageURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
