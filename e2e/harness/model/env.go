package model

import (
	"os"
	"strings"
)

const (
	// EnvE2EArtifactDir selects the directory used for E2E artifacts.
	EnvE2EArtifactDir = "CDP_E2E_ARTIFACT_DIR"
	// EnvE2EBrowser enables browser-backed E2E scenarios.
	EnvE2EBrowser = "CDP_E2E_BROWSER"
	// EnvE2EChrome overrides the browser executable path.
	EnvE2EChrome = "CDP_E2E_CHROME"
	// EnvE2EChromeArgs supplies comma-separated browser arguments.
	EnvE2EChromeArgs = "CDP_E2E_CHROME_ARGS"
	// EnvE2EHeadless controls whether the E2E browser is headless.
	EnvE2EHeadless = "CDP_E2E_HEADLESS"
	// EnvE2EKeepBrowser preserves the E2E browser profile after the run.
	EnvE2EKeepBrowser = "CDP_E2E_KEEP_BROWSER"
)

// LoadBrowserConfigFromEnv loads browser E2E settings from the process environment.
func LoadBrowserConfigFromEnv() BrowserConfig {
	return LoadBrowserConfig(os.LookupEnv)
}

// LoadBrowserConfig loads browser E2E settings with the provided environment lookup.
func LoadBrowserConfig(lookup func(string) (string, bool)) BrowserConfig {
	cfg := BrowserConfig{
		Diagnostics:     parseString(lookup, "CDP_E2E_DIAGNOSTICS"),
		Enabled:         parseBool(lookup, EnvE2EBrowser),
		Executable:      parseString(lookup, EnvE2EChrome),
		Headless:        parseBoolDefaultTrue(lookup, EnvE2EHeadless),
		ExtraArgs:       parseChromeArgs(lookup, EnvE2EChromeArgs),
		KeepUserDataDir: parseBool(lookup, EnvE2EKeepBrowser),
	}
	return cfg
}

func parseString(lookup func(string) (string, bool), key string) string {
	val, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(val)
}

func parseBool(lookup func(string) (string, bool), key string) bool {
	val := strings.ToLower(parseString(lookup, key))
	switch val {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseBoolDefaultTrue(lookup func(string) (string, bool), key string) bool {
	val := strings.ToLower(parseString(lookup, key))
	switch val {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func parseChromeArgs(lookup func(string) (string, bool), key string) []string {
	val := parseString(lookup, key)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	current := ""
	flush := func() {
		if strings.TrimSpace(current) == "" {
			current = ""
			return
		}
		result = append(result, strings.TrimSpace(current))
		current = ""
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if current == "" {
			current = part
			continue
		}
		if strings.HasPrefix(part, "-") {
			flush()
			current = part
			continue
		}
		current += "," + part
	}
	flush()
	return result
}
