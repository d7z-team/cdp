package cdp

import (
	"context"
	"time"
)

// WaitForNetworkIdle waits for no tracked in-flight requests and a quiet interval
// of 500 ms. Persistent requests keep the wait pending until its context expires.
func (p *Page) WaitForNetworkIdle(ctx context.Context) error {
	c, cancel, err := p.operation(ctx, p.timeouts().Navigation)
	if err != nil {
		return err
	}
	defer cancel()
	started := time.Now()
	return operationError("wait_network_idle", poll(c, func() (bool, error) {
		pending, changed := p.engine.NetworkActivity()
		if changed.Before(started) {
			changed = started
		}
		return pending == 0 && time.Since(changed) >= 500*time.Millisecond, nil
	}))
}
