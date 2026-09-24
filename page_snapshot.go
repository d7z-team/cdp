package cdp

import (
	"context"
	"encoding/json"
	"time"

	"gopkg.d7z.net/cdp/snapshot"
)

func copySnapshot(d snapshot.Document) snapshot.Document {
	raw, _ := json.Marshal(d)
	var copy snapshot.Document
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func (p *Page) Snapshot(ctx context.Context) (snapshot.Document, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return snapshot.Document{}, err
	}
	defer cancel()
	v, err := p.engine.CaptureAISnapshot(c)
	return copySnapshot(v), operationError("snapshot", err)
}
func (p *Page) CurrentSnapshot() (snapshot.Document, bool) {
	if p == nil || p.engine == nil {
		return snapshot.Document{}, false
	}
	v, ok := p.engine.CurrentAISnapshot()
	return copySnapshot(v), ok
}

type QuietResult struct {
	Revision int64
	TimedOut bool
}

func (p *Page) WaitForSnapshotQuiet(ctx context.Context, quiet time.Duration) (QuietResult, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return QuietResult{}, err
	}
	defer cancel()
	deadline, _ := c.Deadline()
	v, err := p.engine.WaitForSnapshotQuiet(c, int(quiet.Milliseconds()), int(time.Until(deadline).Milliseconds()))
	return QuietResult{Revision: v.Revision, TimedOut: v.TimedOut}, operationError("snapshot.quiet", err)
}
