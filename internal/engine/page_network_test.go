package engine

import (
	"testing"
)

func TestNetworkActivityFollowsDocumentLifetime(t *testing.T) {
	page := &Page{}
	steps := []struct {
		name   string
		method string
		params map[string]any
		want   int
	}{
		{"old document request", "Network.requestWillBeSent", map[string]any{"requestId": "old", "frameId": "top", "loaderId": "old-loader"}, 1},
		{"new document request before commit", "Network.requestWillBeSent", map[string]any{"requestId": "new", "frameId": "top", "loaderId": "new-loader"}, 2},
		{"commit retires previous loader", "Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "top", "loaderId": "new-loader"}}, 1},
		{"redirect uses same request", "Network.requestWillBeSent", map[string]any{"requestId": "new", "frameId": "top", "loaderId": "new-loader"}, 1},
		{"old completion cannot consume new request", "Network.loadingFailed", map[string]any{"requestId": "old"}, 1},
		{"child request", "Network.requestWillBeSent", map[string]any{"requestId": "child", "frameId": "child-frame", "loaderId": "child-loader"}, 2},
		{"child navigation keeps parent request", "Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "child-frame", "parentId": "top", "loaderId": "next-child-loader"}}, 1},
		{"child next request", "Network.requestWillBeSent", map[string]any{"requestId": "child-next", "frameId": "child-frame", "loaderId": "next-child-loader"}, 2},
		{"detach retires child request", "Page.frameDetached", map[string]any{"frameId": "child-frame"}, 1},
		{"current request completes", "Network.loadingFinished", map[string]any{"requestId": "new"}, 0},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			page.observeNetworkEvent(CDPResponse{Method: step.method, Params: step.params})
			if pending, _ := page.NetworkActivity(); pending != step.want {
				t.Fatalf("pending = %d, want %d", pending, step.want)
			}
		})
	}
	_, quietSince := page.NetworkActivity()
	page.observeNetworkEvent(CDPResponse{Method: "Network.loadingFinished", Params: map[string]any{"requestId": "old"}})
	if _, changed := page.NetworkActivity(); !changed.Equal(quietSince) {
		t.Fatal("late completion of an old document restarted the quiet interval")
	}
}
