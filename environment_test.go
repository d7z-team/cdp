package cdp

import (
	"math"
	"strings"
	"testing"
)

func TestScreenConfiguration(t *testing.T) {
	for _, test := range []struct {
		name    string
		options LaunchOptions
		want    string
		invalid bool
	}{
		{name: "default", want: "--screen-info={1920x1080 devicePixelRatio=1}"},
		{name: "large window", options: LaunchOptions{WindowSize: WindowSize{Width: 2400, Height: 1200}}, want: "--screen-info={2400x1200 devicePixelRatio=1}"},
		{name: "scale", options: LaunchOptions{Screen: ScreenOptions{Width: 1600, Height: 1000, ScaleFactor: 2}}, want: "--screen-info={3200x2000 devicePixelRatio=2}"},
		{name: "physical display", options: LaunchOptions{Headful: true}},
		{name: "headful screen", options: LaunchOptions{Headful: true, Screen: ScreenOptions{Width: 1600, Height: 1000}}, invalid: true},
		{name: "half window", options: LaunchOptions{WindowSize: WindowSize{Width: 1000}}, invalid: true},
		{name: "half screen", options: LaunchOptions{Screen: ScreenOptions{Width: 1000}}, invalid: true},
		{name: "small screen", options: LaunchOptions{Screen: ScreenOptions{Width: 800, Height: 600}, WindowSize: WindowSize{Width: 1440, Height: 960}}, invalid: true},
		{name: "negative scale", options: LaunchOptions{Screen: ScreenOptions{ScaleFactor: -1}}, invalid: true},
		{name: "NaN scale", options: LaunchOptions{Screen: ScreenOptions{ScaleFactor: math.NaN()}}, invalid: true},
		{name: "pixel overflow", options: LaunchOptions{Screen: ScreenOptions{ScaleFactor: math.MaxFloat64}}, invalid: true},
		{name: "infinite scale", options: LaunchOptions{Screen: ScreenOptions{ScaleFactor: math.Inf(1)}}, invalid: true},
		{name: "conflicting screen", options: LaunchOptions{Args: []string{"--screen-info={800x600}"}}, invalid: true},
		{name: "conflicting window", options: LaunchOptions{Args: []string{"--window-size", "800,600"}}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, _, err := test.options.environmentConfig()
			if test.invalid {
				if err == nil {
					t.Fatal("expected invalid configuration")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.want != "" && !strings.Contains(strings.Join(args, " "), test.want) {
				t.Fatalf("args=%v want %q", args, test.want)
			}
			if test.options.Headful && len(args) != 0 {
				t.Fatalf("physical display args: %v", args)
			}
		})
	}
}
