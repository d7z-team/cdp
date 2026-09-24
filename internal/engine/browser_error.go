// Package engine implements the CDP connection, browser manager, and page runtime.
package engine

import (
	"errors"
	"fmt"
	"strings"
)

const (
	browserErrorTextLimit       = 512
	browserErrorDataLimit       = 24
	browserErrorArrayLimit      = 8
	browserErrorCauseDepthLimit = 8
)

// BrowserErrorNode is the JSON-safe browser-side error DTO. It intentionally
// has no error behavior; BrowserError is the Go error wrapper.
type BrowserErrorNode struct {
	Op           string            `json:"op,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Message      string            `json:"message,omitempty"`
	Detail       string            `json:"detail,omitempty"`
	Selector     string            `json:"selector,omitempty"`
	RuntimeID    string            `json:"runtimeId,omitempty"`
	FrameSummary string            `json:"frameSummary,omitempty"`
	Data         map[string]any    `json:"data,omitempty"`
	Cause        *BrowserErrorNode `json:"cause,omitempty"`
}

// BrowserError preserves browser-side failure context as an unwrap-able Go
// error. The embedded node keeps direct field access such as err.Kind available.
type BrowserError struct {
	BrowserErrorNode
	Cause error `json:"-"`
}

func NewBrowserError(op string, kind string, message string, cause error) *BrowserError {
	node := BrowserErrorNode{
		Op:      op,
		Kind:    kind,
		Message: message,
	}
	return BrowserErrorFromNode(node, cause)
}

func BrowserErrorFromNode(node BrowserErrorNode, cause error) *BrowserError {
	clean := sanitizeBrowserErrorNode(node, 0)
	if browserErr := new(BrowserError); cause != nil && errors.As(cause, &browserErr) {
		clean.Cause = &browserErr.BrowserErrorNode
	}
	if cause == nil && clean.Cause != nil {
		cause = &BrowserError{BrowserErrorNode: *clean.Cause}
	}
	return &BrowserError{
		BrowserErrorNode: clean,
		Cause:            cause,
	}
}

func (e *BrowserError) Error() string {
	if e == nil {
		return ""
	}
	message := firstNonEmpty(trimBrowserErrorText(e.Message), trimBrowserErrorText(e.Kind), "browser error")
	if e.Kind != "" && e.Kind != message {
		message = fmt.Sprintf("%s (%s)", message, trimBrowserErrorText(e.Kind))
	}
	if detail := trimBrowserErrorText(e.Detail); detail != "" {
		message += ": " + detail
	}
	if e.FrameSummary != "" {
		message += "; frame=" + trimBrowserErrorText(e.FrameSummary)
	}
	if e.Op != "" {
		return trimBrowserErrorText(e.Op) + ": " + message
	}
	return message
}

func (e *BrowserError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func trimBrowserErrorText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= browserErrorTextLimit {
		return value
	}
	return value[:browserErrorTextLimit] + "...(truncated)"
}

func sanitizeBrowserErrorNode(node BrowserErrorNode, depth int) BrowserErrorNode {
	clean := BrowserErrorNode{
		Op:           trimBrowserErrorText(node.Op),
		Kind:         trimBrowserErrorText(node.Kind),
		Message:      trimBrowserErrorText(node.Message),
		Detail:       trimBrowserErrorText(node.Detail),
		Selector:     trimBrowserErrorText(node.Selector),
		RuntimeID:    trimBrowserErrorText(node.RuntimeID),
		FrameSummary: trimBrowserErrorText(node.FrameSummary),
		Data:         sanitizeBrowserErrorMap(node.Data, depth),
	}
	if node.Cause != nil && depth+1 < browserErrorCauseDepthLimit {
		cause := sanitizeBrowserErrorNode(*node.Cause, depth+1)
		if !cause.empty() {
			clean.Cause = &cause
		}
	}
	return clean
}

func (n BrowserErrorNode) empty() bool {
	return n.Op == "" && n.Kind == "" && n.Message == "" && n.Detail == "" && n.Selector == "" && n.RuntimeID == "" && n.FrameSummary == "" && len(n.Data) == 0 && n.Cause == nil
}

func readBrowserErrorNode(value any) *BrowserErrorNode {
	return readBrowserErrorNodeDepth(value, 0)
}

func readBrowserErrorNodeDepth(value any, depth int) *BrowserErrorNode {
	if depth >= browserErrorCauseDepthLimit {
		return nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	node := sanitizeBrowserErrorNode(BrowserErrorNode{
		Op:           readString(raw["op"]),
		Kind:         readString(raw["kind"]),
		Message:      readString(raw["message"]),
		Detail:       readString(raw["detail"]),
		Selector:     readString(raw["selector"]),
		RuntimeID:    readString(raw["runtimeId"]),
		FrameSummary: readString(raw["frameSummary"]),
		Data:         readBrowserErrorData(raw["data"]),
	}, depth)
	if cause := readBrowserErrorNodeDepth(raw["cause"], depth+1); cause != nil {
		node.Cause = cause
	}
	if node.empty() {
		return nil
	}
	return &node
}

func readBrowserErrorData(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	return sanitizeBrowserErrorMap(raw, 0)
}

func sanitizeBrowserErrorMap(raw map[string]any, depth int) map[string]any {
	if len(raw) == 0 || depth >= browserErrorCauseDepthLimit {
		return nil
	}
	data := make(map[string]any, min(len(raw), browserErrorDataLimit))
	count := 0
	for key, value := range raw {
		if count >= browserErrorDataLimit {
			break
		}
		key = trimBrowserErrorText(key)
		if key == "" {
			continue
		}
		if clean, ok := sanitizeBrowserErrorValue(value, depth+1); ok {
			data[key] = clean
			count++
		}
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

func sanitizeBrowserErrorValue(value any, depth int) (any, bool) {
	switch value := value.(type) {
	case nil:
		return nil, true
	case string:
		return trimBrowserErrorText(value), true
	case bool, int, int32, int64, float32, float64:
		return value, true
	case map[string]any:
		return sanitizeBrowserErrorMap(value, depth), true
	case []any:
		limit := min(len(value), browserErrorArrayLimit)
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			if clean, ok := sanitizeBrowserErrorValue(value[i], depth+1); ok {
				out = append(out, clean)
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func BrowserErrorFromActionability(op string, diagnostic ActionabilityDiagnostic) *BrowserError {
	message := diagnostic.Summary
	if message == "" {
		message = "element is not actionable"
	}
	data := map[string]any{}
	if diagnostic.Element != "" {
		data["element"] = diagnostic.Element
	}
	if diagnostic.Culprit != "" {
		data["culprit"] = diagnostic.Culprit
	}
	if diagnostic.Detail != "" {
		data["detail"] = diagnostic.Detail
	}
	if diagnostic.HasCenter {
		data["center"] = map[string]any{"x": diagnostic.CenterX, "y": diagnostic.CenterY}
	}
	if diagnostic.HasLocalRect {
		data["localRect"] = rectBrowserErrorData(diagnostic.LocalRect)
	}
	if diagnostic.HasTopRect {
		data["topRect"] = rectBrowserErrorData(diagnostic.TopRect)
	}
	if diagnostic.Retriable {
		data["retriable"] = true
	}
	if diagnostic.RetryAction != "" {
		data["retryAction"] = diagnostic.RetryAction
	}
	if diagnostic.HasRetryPoint {
		data["retryPoint"] = map[string]any{"x": diagnostic.RetryPointX, "y": diagnostic.RetryPointY}
	}
	if diagnostic.Stability != nil {
		samples := make([]any, 0, min(len(diagnostic.Stability.Samples), 3))
		for i, sample := range diagnostic.Stability.Samples {
			if i >= 3 {
				break
			}
			samples = append(samples, rectBrowserErrorData(sample))
		}
		data["stability"] = map[string]any{
			"kind":      diagnostic.Stability.Kind,
			"elapsedMs": diagnostic.Stability.ElapsedMs,
			"maxDelta":  rectBrowserErrorData(diagnostic.Stability.MaxDelta),
			"samples":   samples,
		}
	}
	if len(diagnostic.FrameChain) > 0 {
		frames := make([]any, 0, min(len(diagnostic.FrameChain), browserErrorArrayLimit))
		for i, segment := range diagnostic.FrameChain {
			if i >= browserErrorArrayLimit {
				break
			}
			frames = append(frames, map[string]any{
				"frameElement": segment.FrameElement,
				"summary":      segment.Summary,
				"detail":       segment.Detail,
			})
		}
		data["frameChain"] = frames
	}
	node := BrowserErrorNode{
		Op:      op,
		Kind:    firstNonEmpty(diagnostic.Kind, "not_actionable"),
		Message: message,
		Detail:  diagnostic.Detail,
		Data:    data,
	}
	return BrowserErrorFromNode(node, nil)
}

func rectBrowserErrorData(rect Rect) map[string]any {
	return map[string]any{
		"x":      rect.X,
		"y":      rect.Y,
		"width":  rect.Width,
		"height": rect.Height,
	}
}

func BrowserErrorFromCDP(op string, target ExecutionTarget, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBrowserClosed) {
		return err
	}
	if existing := new(BrowserError); errors.As(err, &existing) {
		return err
	}
	data := map[string]any{}
	if target.SessionID != "" {
		data["sessionId"] = target.SessionID
	}
	if target.TargetID != "" {
		data["targetId"] = target.TargetID
	}
	if target.ContextID != 0 {
		data["contextId"] = target.ContextID
	}
	node := BrowserErrorNode{
		Op:        firstNonEmpty(op, "cdp"),
		Kind:      "cdp",
		Message:   err.Error(),
		RuntimeID: target.RuntimeID,
		Data:      data,
	}
	return BrowserErrorFromNode(node, err)
}
