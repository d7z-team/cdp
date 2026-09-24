package engine

import "time"

type networkRequest struct {
	frameID  string
	loaderID string
}

// NetworkActivity observes the requests seen since this page was bound, including
// requests already in progress when a caller starts waiting for network idle.
func (p *Page) NetworkActivity() (pending int, changed time.Time) {
	p.networkMu.Lock()
	defer p.networkMu.Unlock()
	return len(p.networkPending), p.networkChanged
}
func (p *Page) observeNetworkEvent(event CDPResponse) {
	p.networkMu.Lock()
	defer p.networkMu.Unlock()
	switch event.Method {
	case "Network.requestWillBeSent":
		id, _ := event.Params["requestId"].(string)
		if id == "" {
			return
		}
		if p.networkPending == nil {
			p.networkPending = make(map[string]networkRequest)
		}
		frameID, _ := event.Params["frameId"].(string)
		loaderID, _ := event.Params["loaderId"].(string)
		p.networkPending[id] = networkRequest{frameID: frameID, loaderID: loaderID}
	case "Network.loadingFinished", "Network.loadingFailed":
		id, _ := event.Params["requestId"].(string)
		if _, pending := p.networkPending[id]; !pending {
			return
		}
		delete(p.networkPending, id)
	case "Page.frameNavigated":
		frame, ok := event.Params["frame"].(map[string]any)
		if !ok {
			return
		}
		frameID, _ := frame["id"].(string)
		loaderID, _ := frame["loaderId"].(string)
		parentID, _ := frame["parentId"].(string)
		// A new document retires the old document's requests, even when no
		// terminal event reaches this session (for example after a target swap).
		// Its own request may precede frameNavigated, so retain that loader.
		for id, request := range p.networkPending {
			if (parentID == "" || request.frameID == frameID) && request.loaderID != loaderID {
				delete(p.networkPending, id)
			}
		}
	case "Page.frameDetached":
		frameID, _ := event.Params["frameId"].(string)
		for id, request := range p.networkPending {
			if request.frameID == frameID {
				delete(p.networkPending, id)
			}
		}
	default:
		return
	}
	p.networkChanged = time.Now()
}
