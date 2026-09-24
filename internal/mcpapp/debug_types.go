package mcpapp

import (
	"encoding/json"
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

type debugCommand struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Name          string          `json:"name,omitempty"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	Action        string          `json:"action,omitempty"`
	FrameSequence uint64          `json:"frame_sequence,omitempty"`
	X             float64         `json:"x,omitempty"`
	Y             float64         `json:"y,omitempty"`
	ToX           float64         `json:"to_x,omitempty"`
	ToY           float64         `json:"to_y,omitempty"`
	DeltaX        float64         `json:"delta_x,omitempty"`
	DeltaY        float64         `json:"delta_y,omitempty"`
	Key           string          `json:"key,omitempty"`
	Modifiers     []string        `json:"modifiers,omitempty"`
	Text          string          `json:"text,omitempty"`
}

type debugMessage struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	OK        bool            `json:"ok,omitempty"`
	State     *debugTabStatus `json:"state,omitempty"`
	Output    any             `json:"output,omitempty"`
	Events    []Event         `json:"events,omitempty"`
	Error     *ToolError      `json:"error,omitempty"`
	Frame     *debugFrameInfo `json:"frame,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

type debugFrameInfo struct {
	Sequence          uint64  `json:"sequence"`
	ImageWidth        int     `json:"image_width"`
	ImageHeight       int     `json:"image_height"`
	CSSViewportWidth  float64 `json:"css_viewport_width"`
	CSSViewportHeight float64 `json:"css_viewport_height"`
	PageScaleFactor   float64 `json:"page_scale_factor"`
}

type debugTabStatus struct {
	Diagnostics      cdp.DiagnosticsMode `json:"diagnostics"`
	ConsoleCollected bool                `json:"console_collected"`
	TabID            string              `json:"tab_id"`
	URL              string              `json:"url,omitempty"`
	Title            string              `json:"title,omitempty"`
	State            TabState            `json:"state"`
	Phase            operationPhase      `json:"phase"`
	SnapshotID       uint64              `json:"snapshot_id,omitempty"`
	Dialog           *cdp.Dialog         `json:"dialog,omitempty"`
}

type debugControlOutput struct {
	BaseOutput
	Delta *snapshot.Delta `json:"delta,omitempty"`
}
