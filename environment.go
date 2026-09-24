package cdp

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"gopkg.d7z.net/cdp/internal/engine"
)

// ScreenOptions configures the native virtual display used by headless Chrome.
// Width and Height are CSS pixels; ScaleFactor is the device pixel ratio.
// Zero values select a 1920x1080 display at scale 1, enlarged to fit WindowSize.
type ScreenOptions struct {
	Width, Height int
	ScaleFactor   float64
}

func (o LaunchOptions) environmentConfig() ([]string, engine.ScreenConfig, error) {
	var args []string
	config := engine.ScreenConfig{}
	for _, arg := range o.Args {
		key, _, _ := strings.Cut(arg, "=")
		switch key {
		case "--user-agent":
			return nil, config, errors.New("UA override would break native browser identity")
		case "--screen-info", "--window-size":
			return nil, config, fmt.Errorf("%s must be configured through LaunchOptions", key)
		}
	}
	if o.WindowSize.Width < 0 || o.WindowSize.Height < 0 || (o.WindowSize.Width == 0) != (o.WindowSize.Height == 0) {
		return nil, config, errors.New("window size must have two positive dimensions")
	}
	if o.WindowSize.Width > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", o.WindowSize.Width, o.WindowSize.Height))
	}
	screen := o.Screen
	if o.Headful {
		if screen != (ScreenOptions{}) {
			return nil, config, errors.New("virtual screen requires headless mode")
		}
	} else {
		if screen.Width == 0 && screen.Height == 0 {
			screen.Width = max(1920, o.WindowSize.Width)
			screen.Height = max(1080, o.WindowSize.Height)
		}
		if screen.ScaleFactor == 0 {
			screen.ScaleFactor = 1
		}
		if screen.Width <= 0 || screen.Height <= 0 || screen.ScaleFactor <= 0 || math.IsNaN(screen.ScaleFactor) || math.IsInf(screen.ScaleFactor, 0) {
			return nil, config, errors.New("invalid screen dimensions or scale")
		}
		if screen.Width < o.WindowSize.Width || screen.Height < o.WindowSize.Height {
			return nil, config, errors.New("screen must fit the configured window")
		}
		if float64(screen.Width)*screen.ScaleFactor > math.MaxInt32 || float64(screen.Height)*screen.ScaleFactor > math.MaxInt32 {
			return nil, config, errors.New("screen exceeds browser pixel limits")
		}
		args = append(args, fmt.Sprintf("--screen-info={%dx%d devicePixelRatio=%g}", int(math.Ceil(float64(screen.Width)*screen.ScaleFactor)), int(math.Ceil(float64(screen.Height)*screen.ScaleFactor)), screen.ScaleFactor))
		config.Width = screen.Width
		config.Height = screen.Height
		config.Scale = screen.ScaleFactor
	}
	return args, config, nil
}
