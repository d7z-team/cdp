package engine

import (
	"strings"
	"testing"
)

func TestNormalizeNumberInputValue(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "empty", input: "", want: ""},
		{name: "decimal", input: "12.34", want: "12.34"},
		{name: "trim and sign", input: " -12.34 ", want: "-12.34"},
		{name: "leading dot", input: ".5", want: "0.5"},
		{name: "trailing dot", input: "12.", want: "12"},
		{name: "thousands integer", input: "1,234", want: "1234"},
		{name: "thousands decimal", input: "1,234.56", want: "1234.56"},
		{name: "localized decimal rejected", input: "12,34", wantErr: "ambiguous_localized_number"},
		{name: "currency rejected", input: "¥12.34", wantErr: "invalid_number_input"},
		{name: "garbage rejected", input: "1.2.3", wantErr: "invalid_number_input"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeNumberInputValue(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("unexpected normalized value: got %q want %q", got, tc.want)
			}
		})
	}
}
