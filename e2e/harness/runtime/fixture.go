package runtime

import (
	"context"

	"gopkg.d7z.net/cdp/e2e/fixture"
)

// FixtureRuntime owns the HTTP fixture server used by browser scenarios.
type FixtureRuntime struct {
	Server *fixture.Server
}

// StartFixtureRuntime starts the shared fixture server.
func StartFixtureRuntime(ctx context.Context) (*FixtureRuntime, error) {
	return &FixtureRuntime{
		Server: fixture.Start(ctx),
	}, nil
}

// Close stops the fixture server.
func (r *FixtureRuntime) Close() error {
	if r == nil || r.Server == nil {
		return nil
	}
	r.Server.Close()
	return nil
}
