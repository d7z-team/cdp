// Package cdp automates Chromium through the Chrome DevTools Protocol.
//
// Launch owns the process it starts; Connect borrows an existing browser. Close
// releases only owned resources. The context passed to a constructor limits
// connection setup, not the lifetime of the returned Browser.
//
// Browser operations accept a non-nil context and return errors. Locators are
// immutable query plans; Elements identify exact nodes and never retry actions
// against replacement nodes. Concurrent operations are memory-safe, but callers
// must order navigation and actions that depend on each other.
package cdp
