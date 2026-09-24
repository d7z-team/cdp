package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestSelectorCount(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector")
	count := must(page.ByTestID("item").Count(context.Background()))

	if count != 3 {
		t.Fatalf("unexpected item count: %d", count)
	}
	if text := must(page.ByTestID("item").First().TextContent(context.Background())); text != "first" {
		t.Fatalf("unexpected first item text: %q", text)
	}
}

func TestSelectorImageUsesOriginalImgData(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-image")
	img := must(page.ByTestID("scaled-source").ImageSource(context.Background()))

	if img == nil {
		t.Fatal("selector Image returned nil")
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("selector Image should return intrinsic img data, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestSelectorLayeredFallback(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-path")
	text := must(page.Locator("#missing", "#actual").Locator("[data-testid='chosen']").TextContent(context.Background()))

	if text != "chosen-path" {
		t.Fatalf("unexpected layered fallback text: %q", text)
	}
	text = must(page.Locator("#missing >> [data-testid='chosen']", "#actual >> [data-testid='chosen']").TextContent(context.Background()))

	if text != "chosen-path" {
		t.Fatalf("unexpected fallback raw path text: %q", text)
	}
}

func TestSelectorAbsoluteXPathDoesNotEnterShadowRoots(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-path")
	mustOK(page.MouseMove(context.Background(), 12, 12))

	target := page.Locator("#missing >> button", "xpath=/html/body/button[@id='absolute-target']")
	mustOK(target.Click(context.Background()))
	mustOK(target.Click(context.Background()))

	if clicks := attribute(target, "data-clicks"); clicks != "2" {
		t.Fatalf("absolute XPath fallback clicks = %q, want 2", clicks)
	}
	if count := must(page.Locator("#actual").Locator("xpath=/html/body/button[@id='absolute-target']").Count(context.Background())); count != 0 {
		t.Fatalf("chained absolute XPath escaped its scope, count=%d", count)
	}
	if text := must(page.Locator("#shadow-host").Locator("xpath=//button[@id='shadow-target']").TextContent(context.Background())); text != "shadow-target" {
		t.Fatalf("relative XPath in user shadow root = %q", text)
	}
}

func TestSelectorXPathBasic(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector")
	text := must(page.Locator(`xpath=//li[lower-case(normalize-space(.)) = "second"]`).TextContent(context.Background()))

	if text != "second" {
		t.Fatalf("unexpected xpath text: %q", text)
	}
}

func TestSelectorTextExactElementNormalized(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-text")
	textValue := `*6Sample headline with multiple text fragments`
	textSelector := `text-is="` + textValue + `"`
	roleSelector := `role=link[name="` + textValue + `"]`
	xpathSelector := `xpath=//a[normalize-space(.)="` + textValue + `"]`

	for _, selector := range []string{textSelector, roleSelector, xpathSelector} {
		if count := must(page.Locator(selector).Count(context.Background())); count != 1 {
			t.Fatalf("unexpected selector count for %q: %d", selector, count)
		}
		if text := must(page.Locator(selector).TextContent(context.Background())); strings.Join(strings.Fields(text), "") != strings.Join(strings.Fields(textValue), "") {
			t.Fatalf("unexpected selector text for %q: %q", selector, text)
		}
	}
	mustOK(page.Locator(textSelector).Click(context.Background()))

	if got := must(page.ByTestID("news-state").TextContent(context.Background())); got != "news-clicked" {
		t.Fatalf("unexpected text-is click state: %q", got)
	}

	minimalSelector := `text-is="Alpha Beta"`
	if count := must(page.Locator(minimalSelector).Count(context.Background())); count != 1 {
		t.Fatalf("unexpected minimal text selector count: %d", count)
	}
	mustOK(page.Locator(minimalSelector).Click(context.Background()))

	if got := must(page.ByTestID("match-state").TextContent(context.Background())); got != "inner-clicked" {
		t.Fatalf("unexpected minimal text selector click state: %q", got)
	}
}

func TestSelectorXPathPredicates(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-xpath")

	value := must(page.Locator(`xpath=//label[normalize-space(.)=concat("He said ",'"',"go",'"'," and it's fine")]/following::*[self::input or self::select or self::textarea][1]`).Value(context.Background()))

	if value != "quoted-value" {
		t.Fatalf("unexpected concat/following/self label result: %q", value)
	}

	value = must(page.Locator(`xpath=//fieldset[.//legend[normalize-space(.)="Profile Settings"]]//input`).Value(context.Background()))

	if value != "legend-value" {
		t.Fatalf("unexpected fieldset legend anchor result: %q", value)
	}

	text := must(page.Locator(`xpath=//*[self::section or self::article or self::form or @role='dialog'][.//*[normalize-space(.)="Billing Address"]]//button[normalize-space(.)="Submit Order"]`).TextContent(context.Background()))

	if text != "Submit Order" {
		t.Fatalf("unexpected container anchor result: %q", text)
	}

	text = must(page.Locator(`xpath=//*[@role="button" and normalize-space(@aria-label)="Open Panel"]`).TextContent(context.Background()))

	if text != "Open" {
		t.Fatalf("unexpected role aria-label result: %q", text)
	}

	text = must(page.Locator(`xpath=//*[@aria-label and lower-case(normalize-space(@aria-label))="open panel"]`).TextContent(context.Background()))

	if text != "Open" {
		t.Fatalf("unexpected case-insensitive aria-label result: %q", text)
	}

	text = must(page.Locator(`xpath=//*[@role="button" and contains(normalize-space(.), "Launch")]`).TextContent(context.Background()))

	if text != "Launch Control" {
		t.Fatalf("unexpected role text contains result: %q", text)
	}

	text = must(page.Locator(`xpath=//*[@role="button" and lower-case(normalize-space(.))="launch control"]`).TextContent(context.Background()))

	if text != "Launch Control" {
		t.Fatalf("unexpected case-insensitive role text result: %q", text)
	}

	value = must(page.Locator(`xpath=//input[contains(normalize-space(@value), "Send Form")]`).Value(context.Background()))

	if value != "Send Form Now" {
		t.Fatalf("unexpected input @value contains result: %q", value)
	}

	text = must(page.Locator(`xpath=//button[lower-case(normalize-space(.))="mixed case save"]`).TextContent(context.Background()))

	if text != "Mixed Case Save" {
		t.Fatalf("unexpected case-insensitive text result: %q", text)
	}

	text = must(page.Locator(`xpath=//button[some $c in tokenize(normalize-space(@class), '\s+') satisfies $c = "primary"]`).TextContent(context.Background()))

	if text != "Mixed Case Save" {
		t.Fatalf("unexpected class-token xpath result: %q", text)
	}

	text = must(page.Locator(`xpath=//button[every $x in ("cta", "primary") satisfies some $c in tokenize(normalize-space(@class), '\s+') satisfies $c = $x]`).TextContent(context.Background()))

	if text != "Mixed Case Save" {
		t.Fatalf("unexpected class-token combo xpath result: %q", text)
	}

	text = must(page.Locator(`xpath=//li[contains(normalize-space(.), "Second Entry")]`).TextContent(context.Background()))

	if text != "Second Entry" {
		t.Fatalf("unexpected list contains result: %q", text)
	}

	text = must(page.Locator(`xpath=//button[text()[normalize-space()="Direct Only"]]`).TextContent(context.Background()))

	if text != "Direct Only" {
		t.Fatalf("unexpected direct text() result: %q", text)
	}

	text = must(page.Locator(`xpath=//table//tr[*[self::th or self::td][1][contains(normalize-space(.), "Alpha Row")]]/*[self::th or self::td][2]//button[contains(normalize-space(.), "Edit")]`).TextContent(context.Background()))

	if text != "Edit Alpha" {
		t.Fatalf("unexpected table cell selector result: %q", text)
	}
}

func TestSelectorXPathReproHomePrefix(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-xpath-repro")
	selector := `#home >> xpath=//div[normalize-space(.)="[Sample Document 001] Details"]`
	count := must(page.Locator(selector).Count(context.Background()))

	if count != 1 {
		t.Fatalf("unexpected repro selector count: %d", count)
	}
	text := must(page.Locator(selector).TextContent(context.Background()))

	if text != "[Sample Document 001] Details" {
		t.Fatalf("unexpected repro selector text: %q", text)
	}
}

func TestSelectorXPathReproHomePrefixIframe(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-xpath-iframe")
	selector := `#home >> xpath=//div[normalize-space(.)="[Sample Document 001] Details"]`
	count := must(page.Locator(selector).Count(context.Background()))

	if count != 1 {
		t.Fatalf("unexpected iframe repro selector count: %d", count)
	}
	text := must(page.Locator(selector).TextContent(context.Background()))

	if strings.Join(strings.Fields(text), "") != "[SampleDocument001]Details" {
		t.Fatalf("unexpected iframe repro selector text: %q", text)
	}
}

func TestSelectorIndexSampleAndChain(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	if text := must(page.ByTestID("rank-item").Nth(5).TextContent(context.Background())); text != "six" {
		t.Fatalf("unexpected nth(5) text: %q", text)
	}
	if text := must(page.ByTestID("rank-item").Last().TextContent(context.Background())); text != "seven" {
		t.Fatalf("unexpected last() text: %q", text)
	}

	row := page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).Nth(2)
	if text := strings.TrimSpace(must(row.Locator("//td[3]").TextContent(context.Background()))); text != "2026-03-03" {
		t.Fatalf("unexpected chained xpath cell text: %q", text)
	}
	lastRow := page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).Last()
	if text := strings.TrimSpace(must(lastRow.Locator("//td[2]").TextContent(context.Background()))); text != "archived" {
		t.Fatalf("unexpected last() chained xpath cell text: %q", text)
	}
}

func TestSelectorIndexFrameChain(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index-iframe")
	frame := page.Locator("iframe").Nth(1).ContentFrame()
	if text := must(frame.ByTestID("frame-item").First().TextContent(context.Background())); text != "beta-1" {
		t.Fatalf("unexpected second frame first item text: %q", text)
	}
	if text := must(frame.ByTestID("frame-item").Last().TextContent(context.Background())); text != "beta-4" {
		t.Fatalf("unexpected second frame last item text: %q", text)
	}
}

func TestSelectorLoopRespectsTerminalScope(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	third := page.ByTestID("rank-item").Nth(2)
	looped := must(third.All(context.Background()))

	if len(looped) != 1 {
		t.Fatalf("unexpected loop result count after nth: %d", len(looped))
	}
	if text := must(looped[0].TextContent(context.Background())); text != "three" {
		t.Fatalf("unexpected loop[0] text after nth: %q", text)
	}

	if text := must(page.ByTestID("rank-item").Nth(2).Nth(0).TextContent(context.Background())); text != "three" {
		t.Fatalf("unexpected nth-on-nth text: %q", text)
	}

	row := page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).Nth(2).Nth(0)
	if text := strings.TrimSpace(must(row.Locator("//td[1]").TextContent(context.Background()))); text != "gamma" {
		t.Fatalf("unexpected nth-on-nth chained row text: %q", text)
	}
}

func TestSelectorLoopTableRows(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) != 4 {
		t.Fatalf("unexpected looped table row count: %d", len(rows))
	}

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, strings.TrimSpace(must(row.Locator("td:nth-child(1)").TextContent(context.Background()))))
	}
	if joined := strings.Join(got, ","); joined != "alpha,beta,gamma,delta" {
		t.Fatalf("unexpected looped table first column: %q", joined)
	}
}

func TestSelectorLoopTableCells(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) == 0 {
		t.Fatal("expected at least one table row")
	}

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := must(row.Locator("td").All(context.Background()))

		if len(cells) != 3 {
			t.Fatalf("unexpected cell count in row: %d", len(cells))
		}
		parts := make([]string, 0, len(cells))
		for _, cell := range cells {
			parts = append(parts, strings.TrimSpace(must(cell.TextContent(context.Background()))))
		}
		got = append(got, strings.Join(parts, "|"))
	}
	if joined := strings.Join(got, ";"); joined != "alpha|draft|2026-01-01;beta|review|2026-02-02;gamma|published|2026-03-03;delta|archived|2026-04-04" {
		t.Fatalf("unexpected looped table rows/cells: %q", joined)
	}
}

func TestSelectorLoopIframeTableRows(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index-iframe")
	frame := page.Locator("#frame-b").ContentFrame()
	rows := must(frame.Locator(`//table[@data-testid='frame-records']/tbody/tr[@data-testid='frame-record-row']`).All(context.Background()))

	if len(rows) != 3 {
		t.Fatalf("unexpected iframe looped table row count: %d", len(rows))
	}

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := must(row.Locator("td").All(context.Background()))

		if len(cells) != 3 {
			t.Fatalf("unexpected iframe row cell count: %d", len(cells))
		}
		parts := make([]string, 0, len(cells))
		for _, cell := range cells {
			parts = append(parts, strings.TrimSpace(must(cell.TextContent(context.Background()))))
		}
		got = append(got, strings.Join(parts, "|"))
	}
	if joined := strings.Join(got, ";"); joined != "beta|draft|2026-05-01;gamma|review|2026-05-02;delta|published|2026-05-03" {
		t.Fatalf("unexpected iframe looped rows/cells: %q", joined)
	}
}

func TestSelectorLoopXPathChildScope(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) != 4 {
		t.Fatalf("unexpected looped table row count for xpath child scope: %d", len(rows))
	}

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, strings.TrimSpace(must(row.Locator("//td[1]").TextContent(context.Background()))))
	}
	if joined := strings.Join(got, ","); joined != "alpha,beta,gamma,delta" {
		t.Fatalf("unexpected looped xpath child scope values: %q", joined)
	}
}

func TestSelectorLoopIframeXPathChildScope(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index-iframe")
	frame := page.Locator("#frame-b").ContentFrame()
	rows := must(frame.Locator(`//table[@data-testid='frame-records']/tbody/tr[@data-testid='frame-record-row']`).All(context.Background()))

	if len(rows) != 3 {
		t.Fatalf("unexpected iframe looped table row count for xpath child scope: %d", len(rows))
	}

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, strings.TrimSpace(must(row.Locator("//td[2]").TextContent(context.Background()))))
	}
	if joined := strings.Join(got, ","); joined != "draft,review,published" {
		t.Fatalf("unexpected iframe looped xpath child scope values: %q", joined)
	}
}

func TestSelectorLoopImplicitIframeChildChain(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-loop-implicit-iframe")
	rowSelector := `xpath=(//iframe[some $c in tokenize(normalize-space(@class), '\s+') satisfies $c = "iframe"])[1] >> div:nth-of-type(2) > table > tbody > tr`
	directSelector := rowSelector + ` >> .el-icon-view`

	if count := must(page.Locator(directSelector).Count(context.Background())); count != 2 {
		t.Fatalf("unexpected direct selector count: %d", count)
	}
	mustOK(page.Locator(directSelector).First().Click(context.Background()))

	if got := must(page.Locator("#target-frame").ContentFrame().ByTestID("loop-state").TextContent(context.Background())); got != "clicked-1" {
		t.Fatalf("unexpected direct selector click state: %q", got)
	}
	mustOK(page.Locator(rowSelector).Nth(1).Locator(".el-icon-view").Click(context.Background()))

	if got := must(page.Locator("#target-frame").ContentFrame().ByTestID("loop-state").TextContent(context.Background())); got != "clicked-2" {
		t.Fatalf("unexpected nth child selector click state: %q", got)
	}

	index := 0
	for _, item := range must(page.Locator(rowSelector).All(context.Background())) {
		index++
		mustOK(item.Locator(".el-icon-view").Click(context.Background()))

		want := fmt.Sprintf("clicked-%d", index)
		if got := must(page.Locator("#target-frame").ContentFrame().ByTestID("loop-state").TextContent(context.Background())); got != want {
			t.Fatalf("unexpected loop child selector click state at row %d: got %q want %q", index, got, want)
		}
	}
}

func TestSelectorLoopChildCombinator(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) != 4 {
		t.Fatalf("unexpected loop row count: %d", len(rows))
	}

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := must(row.Locator("> td").All(context.Background()))

		if len(cells) != 3 {
			t.Fatalf("unexpected child combinator cell count: %d", len(cells))
		}
		parts := make([]string, 0, len(cells))
		for _, cell := range cells {
			parts = append(parts, strings.TrimSpace(must(cell.TextContent(context.Background()))))
		}
		got = append(got, strings.Join(parts, "|"))
	}
	if joined := strings.Join(got, ";"); joined != "alpha|draft|2026-01-01;beta|review|2026-02-02;gamma|published|2026-03-03;delta|archived|2026-04-04" {
		t.Fatalf("unexpected child combinator loop results: %q", joined)
	}
}

func TestSelectorLoopSiblingCombinator(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) != 4 {
		t.Fatalf("unexpected loop row count: %d", len(rows))
	}

	row := rows[0]
	first := must(row.Locator("> td").All(context.Background()))[0]
	if text := strings.TrimSpace(must(first.TextContent(context.Background()))); text != "alpha" {
		t.Fatalf("unexpected first td text: %q", text)
	}
	next := must(first.Locator("+ td").TextContent(context.Background()))

	if strings.TrimSpace(next) != "draft" {
		t.Fatalf("unexpected adjacent sibling text: %q", strings.TrimSpace(next))
	}

	allRemaining := must(first.Locator("~ td").All(context.Background()))

	if len(allRemaining) != 2 {
		t.Fatalf("unexpected general sibling count: %d", len(allRemaining))
	}
	remainingTexts := make([]string, 0, len(allRemaining))
	for _, sib := range allRemaining {
		remainingTexts = append(remainingTexts, strings.TrimSpace(must(sib.TextContent(context.Background()))))
	}
	if joined := strings.Join(remainingTexts, ","); joined != "draft,2026-01-01" {
		t.Fatalf("unexpected general sibling texts: %q", joined)
	}
}

func TestSelectorLoopCssChildSelector(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) != 4 {
		t.Fatalf("unexpected loop row count: %d", len(rows))
	}

	row := rows[0]
	cell := must(row.Locator("> td:nth-child(2)").TextContent(context.Background()))

	if strings.TrimSpace(cell) != "draft" {
		t.Fatalf("unexpected nth-child(2) text: %q", strings.TrimSpace(cell))
	}

	cell = must(row.Locator("td:nth-last-child(2)").TextContent(context.Background()))

	if strings.TrimSpace(cell) != "draft" {
		t.Fatalf("unexpected nth-last-child(2) text: %q", strings.TrimSpace(cell))
	}
}

func TestSelectorLoopContentFrameChain(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index-iframe")
	frames := must(page.Locator("iframe").All(context.Background()))

	if len(frames) < 2 {
		t.Fatal("expected at least 2 iframes")
	}

	frameB := frames[1].ContentFrame()
	items := must(frameB.ByTestID("frame-item").All(context.Background()))

	if len(items) != 4 {
		t.Fatalf("unexpected items in frame-b: %d", len(items))
	}
	if text := must(items[0].TextContent(context.Background())); text != "beta-1" {
		t.Fatalf("unexpected first frame-b item: %q", text)
	}
	if text := must(items[3].TextContent(context.Background())); text != "beta-4" {
		t.Fatalf("unexpected last frame-b item: %q", text)
	}
}

func TestSelectorLoopEmptyResult(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator("nonexistent-selector-xyz").All(context.Background()))

	if len(rows) != 0 {
		t.Fatalf("unexpected non-empty loop for nonexistent selector: %d", len(rows))
	}
}

func TestSelectorSyntaxErrorReported(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: cdp.ActionFast, Timeouts: cdp.Timeouts{Action: 5000 * time.Millisecond, Read: 5000 * time.Millisecond}})
	started := time.Now()

	hadPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				hadPanic = true
				msg := fmt.Sprintf("%v", r)
				if !strings.Contains(msg, "syntax") {
					t.Fatalf("panic message should contain 'syntax', got: %v", r)
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("panic should preserve error value, got %T", r)
				}
				var selectorErr *cdp.LocatorError
				if !errors.As(err, &selectorErr) {
					t.Fatalf("expected LocatorError, got %#v", err)
				}
				var browserErr *cdp.BrowserError
				if !errors.As(err, &browserErr) || browserErr.Kind != "syntax" {
					t.Fatalf("expected syntax BrowserError, got %#v", err)
				}
			}
		}()
		must(page.Locator("#foo[").TextContent(context.Background()))

	}()
	if !hadPanic {
		t.Fatal("expected panic for invalid CSS selector")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("deterministic selector syntax error was retried for %s", elapsed)
	}
}

func TestSelectorSyntaxErrorFallback(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-path")
	text := must(page.Locator("#foo[", "#actual >> [data-testid='chosen']").TextContent(context.Background()))

	if text != "chosen-path" {
		t.Fatalf("fallback should match after syntax error in option 1, got: %q", text)
	}
}

func TestSelectorFallbackFailureAttempts(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-path")
	page = configuredPage(t, page, cdp.ConnectOptions{ActionMode: cdp.ActionFast, Timeouts: cdp.Timeouts{Action: 200 * time.Millisecond, Read: 200 * time.Millisecond}})

	err := expectPanicError(func() {
		must(page.Locator("#missing-primary", "#missing-secondary").TextContent(context.Background()))

	})
	var selectorErr *cdp.LocatorError
	if !errors.As(err, &selectorErr) {
		t.Fatalf("expected LocatorError, got %#v", err)
	}
	var browserErr *cdp.BrowserError
	if !errors.As(err, &browserErr) || browserErr.Kind != "no_match" {
		t.Fatalf("expected no_match BrowserError, got %#v", err)
	}
	attempts, ok := browserErr.Data["attempts"].([]any)
	if !ok || len(attempts) != 2 {
		t.Fatalf("expected two fallback attempts, got %+v", browserErr.Data["attempts"])
	}
	first, _ := attempts[0].(map[string]any)
	second, _ := attempts[1].(map[string]any)
	if first["selector"] != "#missing-primary" || second["selector"] != "#missing-secondary" {
		t.Fatalf("unexpected fallback attempts: %+v", attempts)
	}
}

func TestSelectorSyntaxErrorLoopChildCombinator(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/selector-index")
	rows := must(page.Locator(`//table[@data-testid='records']/tbody/tr[contains(@class, 'record-row')]`).All(context.Background()))

	if len(rows) != 4 {
		t.Fatalf("unexpected row count: %d", len(rows))
	}

	hadPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				hadPanic = true
				msg := fmt.Sprintf("%v", r)
				if !strings.Contains(msg, "syntax") {
					t.Fatalf("loop child combinator: panic message should contain 'syntax', got: %v", r)
				}
			}
		}()
		must(rows[0].Locator("> #foo[").TextContent(context.Background()))

	}()
	if !hadPanic {
		t.Fatal("expected panic for invalid CSS child combinator selector in loop")
	}
}

func TestSelectorLargeDOMJSGenerated(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	mustOK(page.SetContent(context.Background(), `<!doctype html><html><body><div id="app"></div></body></html>`))
	evalText(page, `
const app = document.getElementById('app');
if (!app) {
  throw new Error('missing app root');
}
const controls = document.createElement('section');
controls.id = 'controls';
controls.innerHTML = '<button data-testid="bulk-action">run-action</button>'
  + '<span data-testid="bulk-state">idle</span>'
  + '<input data-testid="bulk-input" value="">'
  + '<div data-testid="bulk-mirror"></div>'
  + '<div data-testid="bulk-root"></div>';
app.appendChild(controls);

const state = controls.querySelector('[data-testid="bulk-state"]');
const input = controls.querySelector('[data-testid="bulk-input"]');
const mirror = controls.querySelector('[data-testid="bulk-mirror"]');
const root = controls.querySelector('[data-testid="bulk-root"]');

controls.querySelector('[data-testid="bulk-action"]').addEventListener('click', () => {
  state.textContent = 'clicked';
});
input.addEventListener('input', () => {
  mirror.textContent = input.value;
});

const fragment = document.createDocumentFragment();
for (let i = 0; i < 10000; i++) {
  const row = document.createElement('div');
  row.className = 'bulk-row';
  row.setAttribute('data-testid', 'bulk-item');
  row.setAttribute('data-rank', String(i));
  row.innerHTML = '<span class="label">item-' + i + '</span><button data-testid="row-action">row-' + i + '</button>';
  fragment.appendChild(row);
}
root.appendChild(fragment);
`)

	if count := must(page.ByTestID("bulk-item").Count(context.Background())); count != 10000 {
		t.Fatalf("unexpected bulk item count: %d", count)
	}
	if text := must(page.ByTestID("bulk-item").First().Locator(".label").TextContent(context.Background())); text != "item-0" {
		t.Fatalf("unexpected first bulk item label: %q", text)
	}
	if text := must(page.ByTestID("bulk-item").Nth(9999).Locator(".label").TextContent(context.Background())); text != "item-9999" {
		t.Fatalf("unexpected nth bulk item label: %q", text)
	}
	if text := must(page.ByTestID("bulk-item").Last().Locator(".label").TextContent(context.Background())); text != "item-9999" {
		t.Fatalf("unexpected last bulk item label: %q", text)
	}
	mustOK(page.ByTestID("bulk-action").Click(context.Background()))

	if text := must(page.ByTestID("bulk-state").TextContent(context.Background())); text != "clicked" {
		t.Fatalf("unexpected bulk state text: %q", text)
	}
	mustOK(page.ByTestID("bulk-input").Fill(context.Background(), "massive-dom-ok"))
	if text := must(page.ByTestID("bulk-mirror").TextContent(context.Background())); text != "massive-dom-ok" {
		t.Fatalf("unexpected bulk mirror text: %q", text)
	}

	if text := must(page.Locator("#controls", "body").Locator("[data-rank='3456']").Locator(".label").TextContent(context.Background())); text != "item-3456" {
		t.Fatalf("unexpected fallback-scoped label text: %q", text)
	}
}

func TestSelectorActionableBucketScansBeyondRefLimit(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	sess := acquireSession(t)
	page, err := sess.Open("/")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	mustOK(page.SetContent(context.Background(), `<!doctype html><html><head><style>
.hidden-menu { display: none; }
.target-action { margin: 2px; padding: 4px 8px; }
</style></head><body><div id="app"></div><span data-testid="state">idle</span></body></html>`))
	evalText(page, `
const app = document.getElementById('app');
const fragment = document.createDocumentFragment();
for (let i = 0; i < 1000; i++) {
  const menu = document.createElement('ul');
  menu.setAttribute('role', 'menu');
  menu.className = 'hidden-menu';
  const item = document.createElement('li');
  item.setAttribute('role', 'menuitem');
  item.innerHTML = '<button class="target-action" type="button">hidden-' + i + '</button>';
  menu.appendChild(item);
  fragment.appendChild(menu);
}
const visibleMenu = document.createElement('ul');
visibleMenu.setAttribute('role', 'menu');
visibleMenu.setAttribute('data-testid', 'visible-menu');
const visibleItem = document.createElement('li');
visibleItem.setAttribute('role', 'menuitem');
const button = document.createElement('button');
button.className = 'target-action';
button.type = 'button';
button.textContent = 'run target';
button.addEventListener('click', () => {
  document.querySelector('[data-testid="state"]').textContent = 'clicked';
});
visibleItem.appendChild(button);
visibleMenu.appendChild(visibleItem);
fragment.appendChild(visibleMenu);
app.appendChild(fragment);
`)

	if count := must(page.Locator("role=menu >> button.target-action").Count(context.Background())); count != 1001 {
		t.Fatalf("unexpected target action count: %d", count)
	}
	mustOK(page.Locator("role=menu >> button.target-action").Click(context.Background()))

	if text := must(page.ByTestID("state").TextContent(context.Background())); text != "clicked" {
		t.Fatalf("expected click to reach actionable target beyond ref limit, got %q", text)
	}
}

func TestSelectorLargeDOMIframeJSGenerated(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/iframe")
	frame := page.FrameLocator("#demo-frame")
	evalText(frame.Locator("body"), `
this.innerHTML = '<section id="frame-controls">'
  + '<button data-testid="frame-bulk-action">frame-run</button>'
  + '<span data-testid="frame-bulk-state">idle</span>'
  + '<input data-testid="frame-bulk-input" value="">'
  + '<div data-testid="frame-bulk-mirror"></div>'
  + '<div data-testid="frame-bulk-root"></div>'
  + '</section>';
const controls = this.querySelector('#frame-controls');
const state = controls.querySelector('[data-testid="frame-bulk-state"]');
const input = controls.querySelector('[data-testid="frame-bulk-input"]');
const mirror = controls.querySelector('[data-testid="frame-bulk-mirror"]');
const root = controls.querySelector('[data-testid="frame-bulk-root"]');
controls.querySelector('[data-testid="frame-bulk-action"]').addEventListener('click', () => {
  state.textContent = 'clicked';
});
input.addEventListener('input', () => {
  mirror.textContent = input.value;
});
const fragment = document.createDocumentFragment();
for (let i = 0; i < 10000; i++) {
  const row = document.createElement('div');
  row.className = 'frame-bulk-row';
  row.setAttribute('data-testid', 'frame-bulk-item');
  row.setAttribute('data-rank', String(i));
  row.innerHTML = '<span class="label">frame-item-' + i + '</span>';
  fragment.appendChild(row);
}
root.appendChild(fragment);
return this.childElementCount;
`)

	if count := must(frame.ByTestID("frame-bulk-item").Count(context.Background())); count != 10000 {
		t.Fatalf("unexpected frame bulk item count: %d", count)
	}
	if text := must(frame.ByTestID("frame-bulk-item").First().Locator(".label").TextContent(context.Background())); text != "frame-item-0" {
		t.Fatalf("unexpected first frame bulk item label: %q", text)
	}
	if text := must(frame.ByTestID("frame-bulk-item").Nth(4321).Locator(".label").TextContent(context.Background())); text != "frame-item-4321" {
		t.Fatalf("unexpected nth frame bulk item label: %q", text)
	}
	if text := must(frame.ByTestID("frame-bulk-item").Last().Locator(".label").TextContent(context.Background())); text != "frame-item-9999" {
		t.Fatalf("unexpected last frame bulk item label: %q", text)
	}
	mustOK(frame.ByTestID("frame-bulk-action").Click(context.Background()))

	if text := must(frame.ByTestID("frame-bulk-state").TextContent(context.Background())); text != "clicked" {
		t.Fatalf("unexpected frame bulk state text: %q", text)
	}
	mustOK(frame.ByTestID("frame-bulk-input").Fill(context.Background(), "iframe-massive-dom-ok"))
	if text := must(frame.ByTestID("frame-bulk-mirror").TextContent(context.Background())); text != "iframe-massive-dom-ok" {
		t.Fatalf("unexpected frame bulk mirror text: %q", text)
	}
}

func TestTextLocatorExactOption(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	evalText(page, `for(const text of ['quoted "value"','quoted "value" suffix']){const button=document.createElement('button');button.textContent=text;document.body.append(button)}`)
	if got := must(page.ByText(`quoted "value"`, cdp.TextOptions{Exact: true}).Count(context.Background())); got != 1 {
		t.Fatalf("exact text count: %d", got)
	}
	if got := must(page.ByText(`quoted "value"`, cdp.TextOptions{}).Count(context.Background())); got != 2 {
		t.Fatalf("substring text count: %d", got)
	}
}

func TestLabelAndPlaceholderExactOptions(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/home")
	evalText(page, `for(const text of ['name "quoted"','name "quoted" suffix']){const input=document.createElement('input');input.placeholder=text;input.setAttribute('aria-label',text);document.body.append(input)}`)
	for _, query := range []*cdp.Locator{page.ByLabel(`name "quoted"`, cdp.TextOptions{Exact: true}), page.ByPlaceholder(`name "quoted"`, cdp.TextOptions{Exact: true})} {
		if got := must(query.Count(context.Background())); got != 1 {
			t.Fatalf("exact count: %d", got)
		}
	}
	for _, query := range []*cdp.Locator{page.ByLabel(`name "quoted"`, cdp.TextOptions{}), page.ByPlaceholder(`name "quoted"`, cdp.TextOptions{})} {
		if got := must(query.Count(context.Background())); got != 2 {
			t.Fatalf("substring count: %d", got)
		}
	}
}
