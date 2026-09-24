package library

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestWaitToBeVisible(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	sel := page.ByTestID("visible-box")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := sel.Wait(ctx, cdp.StateVisible); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeHiddenStatic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("hidden-box").Wait(ctx, cdp.StateHidden); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeHiddenDelayed(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("delayed-hidden").Wait(ctx, cdp.StateHidden); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeVisibleTimeoutReturnsError(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("hidden-box").Wait(ctx, cdp.StateVisible); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestWaitToBeHiddenTimeoutReturnsError(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("visible-box").Wait(ctx, cdp.StateHidden); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestWaitToHaveText(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("text-static").WaitForText(ctx, "Hello World", cdp.TextOptions{Exact: true}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveTextMismatchReturnsError(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("text-static").WaitForText(ctx, "Wrong Text", cdp.TextOptions{Exact: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestWaitToHaveTextDynamic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("text-dynamic").WaitForText(ctx, "Loaded Successfully", cdp.TextOptions{Exact: true}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToContainText(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("text-contains").WaitForText(ctx, "brown fox", cdp.TextOptions{Exact: false}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToContainTextAbsentReturnsError(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("text-static").WaitForText(ctx, "nonexistent", cdp.TextOptions{Exact: false}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestWaitToBeEnabled(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("btn-enabled").Wait(ctx, cdp.StateEnabled); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeEnabledDelayed(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("btn-delayed-enable").Wait(ctx, cdp.StateEnabled); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeDisabled(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("btn-disabled").Wait(ctx, cdp.StateDisabled); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeDisabledDelayed(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("btn-delayed-disable").Wait(ctx, cdp.StateDisabled); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeChecked(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("cb-checked").Wait(ctx, cdp.StateChecked); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeCheckedDelayed(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("cb-delayed-check").Wait(ctx, cdp.StateChecked); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeEditable(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("input-editable").Wait(ctx, cdp.StateEditable); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeEmpty(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("empty-div").Wait(ctx, cdp.StateEmpty); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToBeEmptyDelayed(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("delayed-empty").Wait(ctx, cdp.StateEmpty); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveCount(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.Locator("[data-testid=list] .item").WaitForCount(ctx, 3); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveCountDynamic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.Locator("[data-testid=list-dynamic] .dyn-item").WaitForCount(ctx, 4); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveValue(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("value-input").WaitForValue(ctx, "initial"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveValueDynamic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("value-delayed").WaitForValue(ctx, "changed"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveAttribute(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("attr-div").WaitForAttribute(ctx, "data-myattr", "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveAttributeDynamic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("attr-dynamic").WaitForAttribute(ctx, "data-status", "done"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveClass(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("class-div").WaitForClass(ctx, "active"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveClassDynamic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("class-dynamic").WaitForClass(ctx, "dynamic-added"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveCSS(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("css-div").WaitForStyle(ctx, "color", "rgb(255, 0, 0)"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveCSSDynamic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("css-dynamic").WaitForStyle(ctx, "color", "rgb(0, 128, 0)"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitToHaveStyle(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("css-div").WaitForStyle(ctx, "display", "block"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitTimeoutReturnsError(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/expect")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := page.ByTestID("never-visible").Wait(ctx, cdp.StateVisible); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}
