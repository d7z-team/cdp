package mcpapp

import (
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

type BaseOutput struct {
	OK       bool       `json:"ok"`
	TabID    string     `json:"tab_id,omitempty"`
	State    TabState   `json:"state,omitempty"`
	Warnings []string   `json:"warnings,omitempty"`
	Error    *ToolError `json:"error,omitempty"`
}

type SnapshotView struct {
	ID       uint64 `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
	Cursor   string `json:"cursor,omitempty"`
	HasMore  bool   `json:"has_more"`
}

type NavigateInput struct {
	TabID string `json:"tab_id,omitempty" jsonschema:"Existing tab ID. Omit to create a tab."`
	URL   string `json:"url" jsonschema:"Absolute URL to navigate to."`
}

type NavigateOutput struct {
	BaseOutput
	Snapshot *SnapshotView `json:"snapshot,omitempty"`
}

type HistoryInput struct {
	TabID  string `json:"tab_id" jsonschema:"Tab ID."`
	Action string `json:"action" jsonschema:"History action: back, forward, or reload."`
}

type HistoryOutput = NavigateOutput

type SnapshotInput struct {
	TabID    string `json:"tab_id" jsonschema:"Tab ID."`
	Cursor   string `json:"cursor,omitempty" jsonschema:"Cursor returned by the current snapshot page."`
	MaxRunes int    `json:"max_runes,omitempty" jsonschema:"Maximum Markdown runes to return; defaults to 32000."`
}

type SnapshotOutput = NavigateOutput

type FindInput struct {
	TabID  string   `json:"tab_id" jsonschema:"Tab ID with a current snapshot."`
	Text   string   `json:"text,omitempty" jsonschema:"Text contained in name, text, or value."`
	Role   string   `json:"role,omitempty" jsonschema:"Exact semantic role."`
	Name   string   `json:"name,omitempty" jsonschema:"Accessible name substring."`
	States []string `json:"states,omitempty" jsonschema:"Required state names such as checked or disabled."`
	Limit  int      `json:"limit,omitempty" jsonschema:"Maximum matches; defaults to 20."`
}

type FindOutput struct {
	BaseOutput
	Matches []snapshot.Match `json:"matches"`
}

type RefInput struct {
	TabID string `json:"tab_id" jsonschema:"Tab ID."`
	Ref   string `json:"ref" jsonschema:"Exact ref from the current snapshot, for example s17/e5."`
}

type TypeInput struct {
	TabID string `json:"tab_id" jsonschema:"Tab ID."`
	Ref   string `json:"ref" jsonschema:"Exact ref from the current snapshot."`
	Text  string `json:"text" jsonschema:"Text to enter."`
	Mode  string `json:"mode,omitempty" jsonschema:"replace (default) or append."`
}

type SelectInput struct {
	TabID  string   `json:"tab_id" jsonschema:"Tab ID."`
	Ref    string   `json:"ref" jsonschema:"Exact select ref from the current snapshot."`
	Values []string `json:"values" jsonschema:"Option values to select."`
}

type CheckInput struct {
	TabID   string `json:"tab_id" jsonschema:"Tab ID."`
	Ref     string `json:"ref" jsonschema:"Exact checkbox or radio ref."`
	Checked bool   `json:"checked" jsonschema:"Desired checked state."`
}

type DragInput struct {
	TabID     string `json:"tab_id" jsonschema:"Tab ID."`
	SourceRef string `json:"source_ref" jsonschema:"Exact source ref."`
	TargetRef string `json:"target_ref" jsonschema:"Exact destination ref."`
}

type PressKeyInput struct {
	TabID     string   `json:"tab_id" jsonschema:"Tab ID."`
	Ref       string   `json:"ref,omitempty" jsonschema:"Optional exact target ref to focus first."`
	Key       string   `json:"key" jsonschema:"Keyboard key."`
	Modifiers []string `json:"modifiers,omitempty" jsonschema:"Alt, Control, Meta, or Shift modifiers."`
}

type ScrollInput struct {
	TabID  string   `json:"tab_id" jsonschema:"Tab ID."`
	Ref    string   `json:"ref,omitempty" jsonschema:"Optional exact scroll container ref."`
	DeltaX float64  `json:"delta_x,omitempty" jsonschema:"Horizontal scroll delta in pixels."`
	DeltaY float64  `json:"delta_y,omitempty" jsonschema:"Vertical scroll delta in pixels."`
	X      *float64 `json:"x,omitempty" jsonschema:"Absolute horizontal position when provided with y."`
	Y      *float64 `json:"y,omitempty" jsonschema:"Absolute vertical position when provided with x."`
}

type UploadInput struct {
	TabID string   `json:"tab_id" jsonschema:"Tab ID."`
	Ref   string   `json:"ref" jsonschema:"Exact file input or associated control ref."`
	Files []string `json:"files" jsonschema:"Absolute local file paths."`
}

type WaitInput struct {
	TabID     string `json:"tab_id" jsonschema:"Tab ID."`
	Condition string `json:"condition" jsonschema:"Condition: time, text, url, or network_idle."`
	Value     string `json:"value,omitempty" jsonschema:"Text or URL substring, or milliseconds for time."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"Maximum wait; defaults to 5000 ms."`
}

type DialogInput struct {
	TabID      string `json:"tab_id" jsonschema:"Tab ID."`
	Action     string `json:"action" jsonschema:"accept, dismiss, or status."`
	PromptText string `json:"prompt_text,omitempty" jsonschema:"Prompt value used when accepting."`
}

type ScreenshotInput struct {
	TabID  string `json:"tab_id" jsonschema:"Tab ID."`
	Ref    string `json:"ref,omitempty" jsonschema:"Optional exact element ref."`
	Format string `json:"format,omitempty" jsonschema:"png (default) or jpeg."`
}

type EvaluateInput struct {
	TabID      string `json:"tab_id" jsonschema:"Tab ID."`
	Ref        string `json:"ref,omitempty" jsonschema:"Optional exact target ref; this is the JS this value."`
	Expression string `json:"expression" jsonschema:"JavaScript expression; promises are awaited."`
}

type TabsInput struct {
	Action string `json:"action" jsonschema:"list, new, select, or close."`
	TabID  string `json:"tab_id,omitempty" jsonschema:"Tab ID for select or close."`
}

type EventsInput struct {
	TabID  string `json:"tab_id" jsonschema:"Tab ID."`
	Cursor uint64 `json:"cursor,omitempty" jsonschema:"Return events after this cursor."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum events; defaults to 100."`
}

type ActionOutput struct {
	BaseOutput
	Delta *snapshot.Delta `json:"delta,omitempty"`
}

type WaitOutput struct {
	BaseOutput
	Snapshot *SnapshotView `json:"snapshot,omitempty"`
}

type DialogOutput struct {
	BaseOutput
	Open   bool        `json:"open"`
	Dialog *cdp.Dialog `json:"dialog,omitempty"`
}

type ScreenshotOutput struct {
	BaseOutput
	MIMEType string `json:"mime_type,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type EvaluateOutput struct {
	BaseOutput
	Value any `json:"value,omitempty"`
}

type TabsOutput struct {
	BaseOutput
	Tabs []TabInfo `json:"tabs"`
}

type EventsOutput struct {
	CollectionStartedAt time.Time `json:"collection_started_at,omitzero"`
	HistoryComplete     bool      `json:"history_complete"`
	BaseOutput
	Events []Event `json:"events"`
	Cursor uint64  `json:"cursor"`
}
