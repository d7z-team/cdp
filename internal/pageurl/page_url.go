// Package pageurl classifies browser URLs for navigation and activation.
package pageurl

import (
	"net/url"
	"strings"
)

// PageURLKind classifies browser page URLs by whether CDP can use them as
// business pages or only as navigation placeholders.
type PageURLKind int

const (
	// PageURLExecutable is a normal page URL that can be selected as the active
	// page and used for runtime evaluation.
	PageURLExecutable PageURLKind = iota
	// PageURLPlaceholder is a blank or browser new-tab page that can carry
	// navigation but should not become the active business page.
	PageURLPlaceholder
	// PageURLInternal is a browser/devtools/extension URL that should be ignored
	// by business-page selection.
	PageURLInternal
)

// ClassifyPageURL returns the page URL kind used by browser activation and CDP
// page selection.
func ClassifyPageURL(pageURL string) PageURLKind {
	raw := strings.TrimSpace(pageURL)
	if raw == "" || raw == "about:blank" {
		return PageURLPlaceholder
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return PageURLExecutable
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "chrome", "edge":
		host := strings.ToLower(parsed.Host)
		if host == "newtab" || host == "new-tab-page" {
			return PageURLPlaceholder
		}
		return PageURLInternal
	case "chrome-search", "edge-search":
		return PageURLPlaceholder
	case "chrome-extension", "devtools", "javascript":
		return PageURLInternal
	default:
		return PageURLExecutable
	}
}

// IsExecutablePageURL reports whether pageURL can be used as a business page.
func IsExecutablePageURL(pageURL string) bool {
	return ClassifyPageURL(pageURL) == PageURLExecutable
}

// IsPlaceholderPageURL reports whether pageURL is a blank or new-tab carrier.
func IsPlaceholderPageURL(pageURL string) bool {
	return ClassifyPageURL(pageURL) == PageURLPlaceholder
}

// IsInternalPageURL reports whether pageURL is an internal browser page.
func IsInternalPageURL(pageURL string) bool {
	return ClassifyPageURL(pageURL) == PageURLInternal
}
