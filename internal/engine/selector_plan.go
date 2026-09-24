package engine

type SelectorResolveMode string

const (
	SelectorResolveModeLoad       SelectorResolveMode = "load"
	SelectorResolveModeActionable SelectorResolveMode = "actionable"
)

type SelectorQueryResultMode string

const (
	SelectorQueryResultModeCount  SelectorQueryResultMode = "count"
	SelectorQueryResultModeSample SelectorQueryResultMode = "sample"
	SelectorQueryResultModeFull   SelectorQueryResultMode = "full"
)

type SelectorQueryBucket string

const (
	SelectorQueryBucketIDs        SelectorQueryBucket = "ids"
	SelectorQueryBucketVisible    SelectorQueryBucket = "visible"
	SelectorQueryBucketActionable SelectorQueryBucket = "actionable"
)

type SelectorPlanOptionKind string

const (
	SelectorPlanOptionKindSelector   SelectorPlanOptionKind = "selector"
	SelectorPlanOptionKindFrameEnter SelectorPlanOptionKind = "frame_enter"
)

type SelectorPlanOption struct {
	Kind SelectorPlanOptionKind `json:"kind"`
	Raw  string                 `json:"raw,omitempty"`
}

type SelectorPlanLayer struct {
	Options []SelectorPlanOption  `json:"options,omitempty"`
	Filter  *SelectorPlanTerminal `json:"filter,omitempty"`
}

type SelectorPlanTerminalKind string

const (
	SelectorPlanTerminalKindNone SelectorPlanTerminalKind = ""
	SelectorPlanTerminalKindNth  SelectorPlanTerminalKind = "nth"
	SelectorPlanTerminalKindLast SelectorPlanTerminalKind = "last"
)

type SelectorPlanTerminal struct {
	Kind  SelectorPlanTerminalKind `json:"kind,omitempty"`
	Index int                      `json:"index"`
}

type SelectorPlan struct {
	Layers   []SelectorPlanLayer   `json:"layers"`
	Terminal *SelectorPlanTerminal `json:"terminal,omitempty"`
}

type SelectorQueryOptions struct {
	Mode          SelectorResolveMode     `json:"mode"`
	ResultMode    SelectorQueryResultMode `json:"resultMode"`
	Limit         int                     `json:"limit"`
	ActionMode    ActionMode              `json:"actionMode,omitempty"`
	ActionPurpose string                  `json:"actionPurpose,omitempty"`
}

type SelectorQueryBucketInfo struct {
	Count int `json:"count"`
}

type SelectorQueryFailure struct {
	Kind         string            `json:"kind,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	Detail       string            `json:"detail,omitempty"`
	FrameSummary string            `json:"frameSummary,omitempty"`
	Error        *BrowserErrorNode `json:"error,omitempty"`
}

func (f *SelectorQueryFailure) BrowserError() *BrowserError {
	if f == nil {
		return nil
	}
	if f.Error == nil {
		return nil
	}
	return BrowserErrorFromNode(*f.Error, nil)
}

type SelectorQuerySession struct {
	Token      string                  `json:"token"`
	RuntimeID  string                  `json:"runtimeId,omitempty"`
	IDs        SelectorQueryBucketInfo `json:"ids"`
	Visible    SelectorQueryBucketInfo `json:"visible"`
	Actionable SelectorQueryBucketInfo `json:"actionable"`
	Truncated  bool                    `json:"truncated"`
	Failure    *SelectorQueryFailure   `json:"failure,omitempty"`
	// Target and RootBackendNodeID are filled by Go when a query is started
	// from an existing selector ref. They let follow-up query operations run in
	// the root object's owning execution context without rediscovering it by
	// runtimeId.
	Target            ExecutionTarget `json:"target,omitempty"`
	RootBackendNodeID int             `json:"rootBackendNodeId,omitempty"`
}

type SelectorTargetRef struct {
	RootID        int                 `json:"rootId"`
	Token         string              `json:"token,omitempty"`
	RuntimeID     string              `json:"runtimeId,omitempty"`
	Bucket        SelectorQueryBucket `json:"bucket,omitempty"`
	Index         int                 `json:"index,omitempty"`
	BackendNodeID int                 `json:"backendNodeId"`
	// Target keeps DOM/runtime commands routed to the frame target that owns
	// BackendNodeID. This is required for iframe and OOPIF query results.
	Target ExecutionTarget `json:"target,omitempty"`
	// Epoch invalidates refs after the owning execution target navigates or is
	// detached.
	Epoch int64 `json:"epoch,omitempty"`
}
