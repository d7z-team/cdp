package library

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.d7z.net/cdp"
)

type inputNumberState struct {
	Value          string   `json:"value"`
	ValueAsNumber  *float64 `json:"valueAsNumber"`
	BadInput       bool     `json:"badInput"`
	StepMismatch   bool     `json:"stepMismatch"`
	RangeOverflow  bool     `json:"rangeOverflow"`
	RangeUnderflow bool     `json:"rangeUnderflow"`
	Valid          bool     `json:"valid"`
}

func readInputNumberState(t *testing.T, page *cdp.Page, testID string) inputNumberState {
	t.Helper()
	raw := must(page.ByTestID(testID).TextContent(context.Background()))

	var state inputNumberState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("unmarshal %s state: %v raw=%q", testID, err, raw)
	}
	return state
}

func requirePanicContains(t *testing.T, fn func(), want string) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected panic containing %q", want)
		}
		if got := fmt.Sprint(recovered); !strings.Contains(got, want) {
			t.Fatalf("expected panic containing %q, got %q", want, got)
		}
	}()
	fn()
}

func TestNumberInputFillDecimal(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	mustOK(page.ByTestID("decimal-number").Fill(context.Background(), "12.34"))
	state := readInputNumberState(t, page, "decimal-number-state")
	if state.Value != "12.34" {
		t.Fatalf("unexpected decimal value: %+v", state)
	}
	if state.ValueAsNumber == nil || *state.ValueAsNumber != 12.34 {
		t.Fatalf("unexpected decimal numeric value: %+v", state)
	}
	if !state.Valid || state.BadInput || state.StepMismatch {
		t.Fatalf("unexpected decimal validity: %+v", state)
	}
}

func TestNumberInputFillLocalizedDecimalRejected(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	requirePanicContains(t, func() {
		mustOK(page.ByTestID("decimal-number").Fill(context.Background(), "12,34"))
	}, "ambiguous_localized_number")
	state := readInputNumberState(t, page, "decimal-number-state")
	if state.Value != "" {
		t.Fatalf("expected rejected localized decimal to leave number input empty, got %+v", state)
	}
	if state.ValueAsNumber != nil {
		t.Fatalf("expected rejected localized decimal to produce no numeric value, got %+v", state)
	}
}

func TestNumberInputFillCurrencyRejected(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	requirePanicContains(t, func() {
		mustOK(page.ByTestID("amount-number").Fill(context.Background(), "¥12.34"))
	}, "invalid_number_input")
	state := readInputNumberState(t, page, "amount-number-state")
	if state.Value != "" {
		t.Fatalf("expected rejected currency string to leave number input empty, got %+v", state)
	}
	if state.ValueAsNumber != nil {
		t.Fatalf("expected rejected currency string to produce no numeric value, got %+v", state)
	}
}

func TestMoneyTextFillCurrencyPreserved(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	mustOK(page.ByTestID("money-text").Fill(context.Background(), "¥12.34"))
	state := readInputNumberState(t, page, "money-text-state")
	if state.Value != "¥12.34" {
		t.Fatalf("expected text money input to preserve currency string, got %+v", state)
	}
}

func TestCompositeNumberInputClickAndFill(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	input := page.ByTestID("composite-number-input")
	mustOK(input.Click(context.Background()))

	if got := evalValue(page, `return document.activeElement?.getAttribute('data-testid') || ''`); got != `"composite-number-input"` {
		t.Fatalf("expected composite number input to receive focus, got %q", got)
	}
	mustOK(input.Fill(context.Background(), "88.12"))
	state := readInputNumberState(t, page, "composite-number-input-state")
	if state.Value != "88.12" {
		t.Fatalf("unexpected composite value: %+v", state)
	}
}

func TestCompositeDisabledInnerInputClickRetargetsToShell(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	mustOK(page.ByTestID("disabled-inner-input").Click(context.Background()))

	if got := must(page.ByTestID("disabled-inner-state").TextContent(context.Background())); got != "activated" {
		t.Fatalf("expected disabled inner input click to activate shell, got %q", got)
	}
}

func TestCompositeDisabledInnerInputFillStillFails(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	requirePanicContains(t, func() {
		mustOK(page.ByTestID("disabled-inner-input").Fill(context.Background(), "12"))
	}, "Element is disabled")
}

func TestCompositeAriaDisabledClickStillRunsWhenDOMAllows(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	mustOK(page.ByTestID("aria-disabled-inner-input").Click(context.Background()))

	if got := must(page.ByTestID("aria-disabled-state").TextContent(context.Background())); got != "activated" {
		t.Fatalf("aria-disabled composite should activate when DOM allows click, got %q", got)
	}
}

func TestCompositeAriaDisabledInputStillFillsWhenDOMAllows(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	mustOK(page.ByTestID("aria-disabled-inner-input").Fill(context.Background(), "42"))
	state := readInputNumberState(t, page, "aria-disabled-input-state")
	if state.Value != "42" {
		t.Fatalf("aria-disabled input should fill when DOM allows input, got %+v", state)
	}
}

func TestNativeDisabledButtonClickStillFails(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	requirePanicContains(t, func() {
		mustOK(page.ByTestID("native-disabled-button").Click(context.Background()))

	}, "Element is disabled")
	if got := must(page.ByTestID("native-disabled-button-state").TextContent(context.Background())); got != "idle" {
		t.Fatalf("native disabled button should not activate, got %q", got)
	}
}

func TestNodeInputInsertTextDecimal(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	element := must(page.ByTestID("decimal-number").All(context.Background()))[0]
	if err := element.Fill(context.Background(), "12.34"); err != nil {
		t.Fatalf("node input decimal: %v", err)
	}
	state := readInputNumberState(t, page, "decimal-number-state")
	if state.Value != "12.34" {
		t.Fatalf("unexpected decimal value: %+v", state)
	}
	if state.ValueAsNumber == nil || *state.ValueAsNumber != 12.34 {
		t.Fatalf("unexpected decimal numeric value: %+v", state)
	}
}

func TestNodeInputInsertTextLocalizedDecimalRejected(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	page := openFixture(t, "/input-number")
	element := must(page.ByTestID("decimal-number").All(context.Background()))[0]
	if err := element.Fill(context.Background(), "12,34"); err == nil {
		t.Fatal("expected localized decimal input to fail")
	} else if got := err.Error(); !strings.Contains(got, "ambiguous_localized_number") {
		t.Fatalf("expected ambiguous_localized_number error, got %q", got)
	}
	state := readInputNumberState(t, page, "decimal-number-state")
	if state.Value != "" {
		t.Fatalf("expected rejected localized decimal to leave number input empty, got %+v", state)
	}
	if state.ValueAsNumber != nil {
		t.Fatalf("expected rejected localized decimal to produce no numeric value, got %+v", state)
	}
}
