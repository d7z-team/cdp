package library

import (
	"context"
	"log"
	"os"
	"testing"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/e2e/harness/model"
	harnessruntime "gopkg.d7z.net/cdp/e2e/harness/runtime"
)

var (
	fixtureRT      *harnessruntime.FixtureRuntime
	execBrowser    *harnessruntime.BrowserRuntime
	execPool       *harnessruntime.WorkerPool
	browserEnabled bool
)

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) (code int) {
	ctx := context.Background()
	cfg := model.LoadBrowserConfigFromEnv()
	code = 1
	defer func() {
		if execPool != nil {
			execPool.Close()
		}
		if execBrowser != nil {
			if err := execBrowser.Close(); err != nil {
				log.Printf("close exec browser: %v", err)
				if code == 0 {
					code = 1
				}
			}
		}
		if fixtureRT != nil {
			if err := fixtureRT.Close(); err != nil {
				log.Printf("close fixture: %v", err)
				if code == 0 {
					code = 1
				}
			}
		}
	}()

	if cfg.Enabled {
		browserEnabled = true
		var err error

		fixtureRT, err = harnessruntime.StartFixtureRuntime(ctx)
		if err != nil {
			log.Printf("start fixture: %v", err)
			return
		}

		execBrowser, err = harnessruntime.StartBrowserRuntime(ctx, cfg)
		if err != nil {
			log.Printf("start exec browser: %v", err)
			return
		}
		execPool, err = harnessruntime.NewWorkerPool(
			harnessruntime.APIBrowserFactory{Browser: execBrowser.APIBrowser}, 1,
		)
		if err != nil {
			log.Printf("create exec pool: %v", err)
			return
		}
	}

	code = m.Run()
	return
}

func acquireSession(t *testing.T) *harnessruntime.BrowserSession {
	t.Helper()
	lease, err := execPool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire page lease: %v", err)
	}
	t.Cleanup(lease.Release)
	return &harnessruntime.BrowserSession{
		Lease:   lease,
		Browser: execBrowser,
		Fixture: fixtureRT,
	}
}

func openFixture(t *testing.T, path string) *cdp.Page {
	t.Helper()
	sess := acquireSession(t)
	page, err := sess.Open(path)
	if err != nil {
		t.Fatalf("open fixture %q: %v", path, err)
	}
	return page
}
