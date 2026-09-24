package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"gopkg.d7z.net/cdp/internal/webassets"
	"gopkg.d7z.net/cdp/snapshot"
)

var nextAISnapshotID atomic.Uint64

type AISnapshotSession struct {
	Document snapshot.Document
}

type snapshotCaptureResult struct {
	snapshot.Document
	NextID int `json:"next_id"`
}

type SnapshotQuietResult struct {
	Revision int64 `json:"revision"`
	TimedOut bool  `json:"timed_out"`
}

func (p *Page) CaptureAISnapshot(ctx context.Context) (snapshot.Document, error) {
	if p == nil {
		return snapshot.Document{}, errors.New("page is nil")
	}
	if ctx == nil {
		return snapshot.Document{}, errors.New("snapshot context is nil")
	}
	if err := p.RefreshShadowRoots(ctx); err != nil {
		return snapshot.Document{}, err
	}
	id := nextAISnapshotID.Add(1)
	handle, err := p.ContextWithContext(ctx, RuntimeContextOptions{Namespace: NamespaceIsolatedCore})
	if err != nil {
		return snapshot.Document{}, fmt.Errorf("snapshot runtime context: %w", err)
	}
	expression := "return " + webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI,
		"captureSnapshot",
		"core snapshot helper is not available",
		webassets.JSLit(map[string]any{"snapshot_id": id, "next_id": 1, "depth": 0}),
	)
	raw, err := handle.EvalJSONContext(ctx, expression)
	if err != nil {
		return snapshot.Document{}, fmt.Errorf("capture snapshot: %w", err)
	}
	var result snapshotCaptureResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return snapshot.Document{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if result.ID != id {
		return snapshot.Document{}, fmt.Errorf("snapshot id mismatch: got %d, want %d", result.ID, id)
	}
	p.snapshotMu.Lock()
	p.currentSnapshot = &AISnapshotSession{Document: result.Document}
	p.snapshotMu.Unlock()
	return result.Document, nil
}

func (p *Page) CurrentAISnapshot() (snapshot.Document, bool) {
	if p == nil {
		return snapshot.Document{}, false
	}
	p.snapshotMu.RLock()
	defer p.snapshotMu.RUnlock()
	if p.currentSnapshot == nil {
		return snapshot.Document{}, false
	}
	return p.currentSnapshot.Document, true
}

func (p *Page) ResolveAISnapshotRef(ctx context.Context, value string) (SelectorTargetRef, error) {
	snapshotID, elementID, err := snapshot.ParseRef(value)
	if err != nil {
		return SelectorTargetRef{}, NewBrowserError("snapshot.resolve", "invalid_argument", err.Error(), err)
	}
	p.snapshotMu.RLock()
	if p.currentSnapshot == nil || p.currentSnapshot.Document.ID != snapshotID {
		p.snapshotMu.RUnlock()
		return SelectorTargetRef{}, NewBrowserError("snapshot.resolve", "stale_target", "snapshot ref is no longer current", nil)
	}
	node, ok := p.currentSnapshot.Document.NodeByID(elementID)
	p.snapshotMu.RUnlock()
	if !ok || node.RuntimeID == "" {
		return SelectorTargetRef{}, NewBrowserError("snapshot.resolve", "stale_target", "snapshot element is no longer available", nil)
	}
	target, err := p.executionTargetForRuntimeContext(ctx, node.RuntimeID)
	if err != nil {
		return SelectorTargetRef{}, NewBrowserError("snapshot.resolve", "stale_target", "snapshot execution context is no longer available", err)
	}
	result, err := p.RuntimeEvaluateOnTarget(ctx, target, `(() => `+webassets.RuntimeOptionalMethodCall(
		webassets.RuntimeFFI,
		"snapshotNode",
		webassets.JSLit(snapshotID),
		webassets.JSLit(elementID),
	)+`)()`, false)
	if err != nil {
		return SelectorTargetRef{}, fmt.Errorf("resolve snapshot node: %w", err)
	}
	objectID, ok := SafeGet[string](result, "result", "objectId")
	if !ok || objectID == "" {
		return SelectorTargetRef{}, NewBrowserError("snapshot.resolve", "stale_target", "snapshot element is detached", nil)
	}
	description, err := p.DescribeNodeByObjectIDInTargetContext(ctx, target, objectID)
	releaseErr := p.ReleaseObjectInTargetContext(ctx, target, objectID)
	if err != nil {
		return SelectorTargetRef{}, fmt.Errorf("describe snapshot node: %w", err)
	}
	if releaseErr != nil && !isMissingObjectErr(releaseErr) {
		return SelectorTargetRef{}, fmt.Errorf("release snapshot node: %w", releaseErr)
	}
	p.rememberNodeIdentityInTarget(target, description)
	_, backendNodeID, ok := parseNodeIdentity(description)
	if !ok || backendNodeID <= 0 {
		return SelectorTargetRef{}, NewBrowserError("snapshot.resolve", "target_detached", "snapshot element has no backend node", nil)
	}
	return SelectorTargetRef{
		RuntimeID: node.RuntimeID, BackendNodeID: backendNodeID, Target: target,
		Epoch: p.currentTargetEpoch(target),
	}, nil
}

func (p *Page) WaitForSnapshotQuiet(ctx context.Context, quietMS, timeoutMS int) (SnapshotQuietResult, error) {
	if ctx == nil {
		return SnapshotQuietResult{}, errors.New("snapshot quiet context is nil")
	}
	if err := p.RefreshShadowRoots(ctx); err != nil {
		return SnapshotQuietResult{}, err
	}
	handle, err := p.ContextWithContext(ctx, RuntimeContextOptions{Namespace: NamespaceIsolatedCore})
	if err != nil {
		return SnapshotQuietResult{}, err
	}
	raw, err := handle.EvalJSONContext(ctx, "return "+webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI, "waitForSnapshotQuiet", "core snapshot helper is not available",
		webassets.JSLit(quietMS), webassets.JSLit(timeoutMS),
	))
	if err != nil {
		return SnapshotQuietResult{}, err
	}
	var result SnapshotQuietResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return SnapshotQuietResult{}, err
	}
	return result, nil
}
