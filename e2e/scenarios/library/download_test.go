package library

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDownloadBlobAndCachedResult(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/download")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watcher := must(page.WatchDownloads(ctx, 1))
	defer watcher.Close()
	mustOK(page.ByTestID("download-blob-btn").Click(ctx))
	files := must(watcher.Wait(ctx))
	if len(files) != 1 || files[0].Name != "blob-file.txt" || string(files[0].Data) != "hello from blob" {
		t.Fatalf("blob: %+v", files)
	}
	files[0].Data[0] = 'x'
	again := must(watcher.Wait(ctx))
	if string(again[0].Data) != "hello from blob" {
		t.Fatal("cached result was mutated")
	}
}

func TestDownloadAnchor(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/download")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watcher := must(page.WatchDownloads(ctx, 1))
	defer watcher.Close()
	mustOK(page.ByTestID("download-anchor").Click(ctx))
	files := must(watcher.Wait(ctx))
	if len(files) != 1 || len(files[0].Data) == 0 || (files[0].Name != "sample.txt" && files[0].Name != "sample") {
		t.Fatalf("anchor: %+v", files)
	}
}

func TestDownloadsPreserveIdenticalNames(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/download")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watcher := must(page.WatchDownloads(ctx, 3))
	defer watcher.Close()
	evalText(page, `for(let i=0;i<3;i++)setTimeout(()=>{const a=document.createElement('a');a.href=URL.createObjectURL(new Blob(['payload-'+i]));a.download='same.txt';a.click();URL.revokeObjectURL(a.href)},100*i)`)
	files := must(watcher.Wait(ctx))
	seen := map[string]bool{}
	for _, file := range files {
		if file.Name != "same.txt" {
			t.Fatalf("name: %q", file.Name)
		}
		seen[string(file.Data)] = true
	}
	if len(files) != 3 || len(seen) != 3 {
		t.Fatalf("same-name downloads lost: %+v", files)
	}
}

func TestDownloadTimeoutReturnsPartialResults(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/download")
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	watcher := must(page.WatchDownloads(ctx, 2))
	defer watcher.Close()
	mustOK(page.ByTestID("download-blob-btn").Click(ctx))
	files, err := watcher.Wait(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || len(files) != 1 {
		t.Fatalf("partial downloads: %+v, %v", files, err)
	}
	next := must(page.WatchDownloads(context.Background(), 1))
	mustOK(next.Close())
}
