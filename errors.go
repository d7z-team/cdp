package cdp

import (
	"errors"
	"fmt"
	"time"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

var (
	ErrDiagnosticsDisabled = errors.New("runtime diagnostics disabled")
	ErrClosed              = errors.New("browser or page closed")
	ErrNotFound            = errors.New("not found")
	ErrStaleElement        = errors.New("stale element")
	ErrProfileInUse        = errors.New("browser profile already in use")
)

type OperationError struct {
	Op      string
	Timeout time.Duration
	Cause   error
}

func (e *OperationError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Cause) }
func (e *OperationError) Unwrap() error { return e.Cause }

type BrowserError struct {
	Op, Kind, Message, Detail, Selector, RuntimeID, FrameSummary string
	Data                                                         map[string]any
	Cause                                                        error
}

func (e *BrowserError) Error() string { return fmt.Sprintf("%s: %s (%s)", e.Op, e.Message, e.Kind) }
func (e *BrowserError) Unwrap() error { return e.Cause }
func operationError(op string, err error) error {
	if err == nil {
		return nil
	}
	err = publicCause(err)
	if errors.Is(err, engine.ErrBrowserClosed) {
		err = errors.Join(ErrClosed, err)
	}
	return &OperationError{Op: op, Cause: err}
}

type MatchCounts struct{ IDs, Visible, Actionable int }
type LocatorError struct {
	Op, Selector string
	Timeout      time.Duration
	Counts       MatchCounts
	Cause        error
}

func (e *LocatorError) Error() string { return fmt.Sprintf("%s: %s: %v", e.Op, e.Selector, e.Cause) }
func (e *LocatorError) Unwrap() error { return e.Cause }

// Convert browser diagnostics recursively so callers never need internal error types.
func publicCause(err error) error {
	if err == nil {
		return nil
	}
	if be, ok := err.(*engine.BrowserError); ok {
		cause := publicCause(be.Cause)
		if be.BrowserErrorNode.Cause != nil {
			cause = errors.Join(publicBrowserNode(*be.BrowserErrorNode.Cause), cause)
		}
		if be.Kind == "stale_target" || be.Kind == "stale_ref" {
			cause = errors.Join(ErrStaleElement, cause)
		}
		return &BrowserError{Op: be.Op, Kind: be.Kind, Message: be.Message, Detail: be.Detail, Selector: be.Selector, RuntimeID: be.RuntimeID, FrameSummary: be.FrameSummary, Data: be.Data, Cause: cause}
	}
	switch e := err.(type) {
	case *OperationError, *LocatorError, *BrowserError:
		return err
	case interface{ Unwrap() []error }:
		children := e.Unwrap()
		out := make([]error, len(children))
		for i, c := range children {
			out[i] = publicCause(c)
		}
		return errors.Join(out...)
	case interface{ Unwrap() error }:
		return &wrappedCause{message: err.Error(), cause: publicCause(e.Unwrap())}
	default:
		return err
	}
}

type wrappedCause struct {
	message string
	cause   error
}

func (e *wrappedCause) Error() string { return e.message }
func (e *wrappedCause) Unwrap() error { return e.cause }
func publicBrowserNode(n engine.BrowserErrorNode) error {
	var cause error
	if n.Cause != nil {
		cause = publicBrowserNode(*n.Cause)
	}
	return &BrowserError{Op: n.Op, Kind: n.Kind, Message: n.Message, Detail: n.Detail, Selector: n.Selector, RuntimeID: n.RuntimeID, FrameSummary: n.FrameSummary, Data: n.Data, Cause: cause}
}
