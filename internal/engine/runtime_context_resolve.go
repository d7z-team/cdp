package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const runtimeContextResolveInterval = 25 * time.Millisecond

func (p *Page) resolveRuntimeContext(ctx context.Context, opts RuntimeContextOptions) (RuntimeContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.checkConnContext(ctx); err != nil {
		return RuntimeContext{}, err
	}
	opts.Namespace = normalizeRuntimeNamespace(opts.Namespace)
	if opts.Timeout <= 0 {
		opts.Timeout = 800 * time.Millisecond
	}
	deadline := time.Now().Add(opts.Timeout)

	for {
		if info, ok := p.findRuntimeContext(opts); ok {
			return info, nil
		}
		if err := p.ensureRuntimeContext(ctx, opts); err != nil {
			return RuntimeContext{}, err
		}
		if info, ok := p.findRuntimeContext(opts); ok {
			return info, nil
		}
		if time.Now().After(deadline) {
			return RuntimeContext{}, fmt.Errorf("runtime context not found: namespace=%s frame=%s", opts.Namespace, opts.FrameID)
		}
		select {
		case <-ctx.Done():
			return RuntimeContext{}, ctx.Err()
		case <-time.After(runtimeContextResolveInterval):
		}
	}
}

func (p *Page) findRuntimeContext(opts RuntimeContextOptions) (RuntimeContext, bool) {
	if p == nil {
		return RuntimeContext{}, false
	}
	reg := NamespaceRegistration{Namespace: NamespacePageMain, MainWorld: true}
	if p.manager != nil {
		reg = p.manager.namespaceRegistration(opts.Namespace)
	}
	frameID := strings.TrimSpace(opts.FrameID)
	if frameID == "" {
		frameID = p.mainFrame()
	}
	// An unknown root frame must never degrade into selecting an arbitrary
	// same-process iframe context. The root page event or top runtime handshake
	// supplies the authoritative FrameId.
	if frameID == "" {
		if opts.Namespace == NamespacePageMain {
			return RuntimeContext{
				Namespace: NamespacePageMain,
				PageID:    p.ID,
				IsDefault: true,
			}, true
		}
		return RuntimeContext{}, false
	}
	preferredSession := ""
	if p.manager != nil {
		for _, session := range p.manager.targetSessionsSnapshot() {
			if session.Type == "iframe" && session.PageID == p.ID && session.TargetID == frameID {
				preferredSession = session.SessionID
				break
			}
		}
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	type candidate struct {
		key    targetContextKey
		info   executionContextInfo
		target ExecutionTarget
	}
	candidates := make([]candidate, 0, len(p.executionContexts))
	for key, info := range p.executionContexts {
		if key.SessionID != preferredSession {
			continue
		}
		if !namespaceMatchesContext(reg, info) {
			continue
		}
		target := p.executionTargetsByContext[key]
		candidateFrame := firstNonEmpty(info.FrameID, target.FrameID)
		if frameID != "" && candidateFrame != frameID {
			continue
		}
		candidates = append(candidates, candidate{key: key, info: info, target: target})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if (candidates[i].key.SessionID == "") != (candidates[j].key.SessionID == "") {
			return candidates[i].key.SessionID == ""
		}
		if candidates[i].key.SessionID != candidates[j].key.SessionID {
			return candidates[i].key.SessionID < candidates[j].key.SessionID
		}
		return candidates[i].key.ContextID > candidates[j].key.ContextID
	})
	if len(candidates) > 0 {
		selected := candidates[0]
		return RuntimeContext{
			Namespace: opts.Namespace,
			PageID:    firstNonEmpty(selected.target.PageID, p.ID),
			TargetID:  selected.target.TargetID,
			SessionID: selected.key.SessionID,
			FrameID:   firstNonEmpty(selected.info.FrameID, selected.target.FrameID),
			ContextID: selected.key.ContextID,
			WorldName: reg.WorldName,
			IsDefault: selected.info.IsDefault,
		}, true
	}
	if opts.Namespace == NamespacePageMain {
		return RuntimeContext{
			Namespace: NamespacePageMain,
			PageID:    p.ID,
			FrameID:   frameID,
			IsDefault: true,
		}, true
	}
	return RuntimeContext{}, false
}

func (p *Page) runtimeContexts(namespace RuntimeNamespace) []RuntimeContext {
	if p == nil {
		return nil
	}
	namespace = normalizeRuntimeNamespace(namespace)
	reg := NamespaceRegistration{Namespace: NamespacePageMain, MainWorld: true}
	if p.manager != nil {
		reg = p.manager.namespaceRegistration(namespace)
	}
	p.contextMu.RLock()
	defer p.contextMu.RUnlock()
	contexts := make([]RuntimeContext, 0)
	for key, info := range p.executionContexts {
		if !namespaceMatchesContext(reg, info) {
			continue
		}
		target := p.executionTargetsByContext[key]
		contexts = append(contexts, RuntimeContext{
			Namespace: namespace,
			PageID:    firstNonEmpty(target.PageID, p.ID),
			TargetID:  target.TargetID,
			SessionID: target.SessionID,
			FrameID:   firstNonEmpty(info.FrameID, target.FrameID),
			ContextID: key.ContextID,
			WorldName: reg.WorldName,
			IsDefault: info.IsDefault,
		})
	}
	sort.Slice(contexts, func(i, j int) bool {
		if (contexts[i].SessionID == "") != (contexts[j].SessionID == "") {
			return contexts[i].SessionID == ""
		}
		if contexts[i].SessionID != contexts[j].SessionID {
			return contexts[i].SessionID < contexts[j].SessionID
		}
		return contexts[i].ContextID > contexts[j].ContextID
	})
	return contexts
}

func (p *Page) ensureRuntimeContext(ctx context.Context, opts RuntimeContextOptions) error {
	if p == nil {
		return errors.New("page is nil")
	}
	if p.manager == nil {
		return nil
	}
	if _, ok := p.findRuntimeContext(opts); ok {
		return nil
	}
	reg := p.manager.namespaceRegistration(opts.Namespace)
	if reg.MainWorld || reg.Namespace == NamespaceOverlay {
		return nil
	}
	frameID := strings.TrimSpace(opts.FrameID)
	if frameID == "" {
		frameID = p.currentMainFrameID(ctx)
	}
	if frameID == "" || reg.WorldName == "" {
		return nil
	}
	if err := p.manager.reconcileDocumentRuntime(ctx, p, ""); err != nil {
		return err
	}
	for _, session := range p.manager.targetSessionsSnapshot() {
		if session.Type == "iframe" && session.PageID == p.ID {
			if err := p.manager.reconcileDocumentRuntime(ctx, p, session.SessionID); err != nil && !isSupersededDocumentError(err) {
				return err
			}
		}
	}

	return nil
}

func (p *Page) currentMainFrameID(_ context.Context) string {
	if p == nil {
		return ""
	}
	return p.mainFrame()
}

func (p *Page) runtimeTargetForNamespaceFrame(ctx context.Context, namespace RuntimeNamespace, frameID string) (ExecutionTarget, error) {
	frameID = strings.TrimSpace(frameID)
	if frameID == "" {
		return ExecutionTarget{}, errors.New("runtime frame id is empty")
	}
	info, err := p.resolveRuntimeContext(ctx, RuntimeContextOptions{
		Namespace: namespace,
		FrameID:   frameID,
	})
	if err != nil {
		return ExecutionTarget{}, err
	}
	return ExecutionTarget{
		PageID:    info.PageID,
		TargetID:  info.TargetID,
		SessionID: info.SessionID,
		ContextID: info.ContextID,
		FrameID:   info.FrameID,
	}, nil
}

func (p *Page) mainFrameRuntimeTarget(ctx context.Context, namespace RuntimeNamespace) (ExecutionTarget, error) {
	frameID := p.mainFrame()
	if frameID == "" && p.manager != nil && IsExecutablePageURL(p.manager.TopPageURL(p.ID)) {
		waitTimeout := p.timeout
		if waitTimeout <= 0 {
			waitTimeout = 10 * time.Second
		}
		parent := ctx
		waitCtx, cancel := context.WithTimeout(parent, waitTimeout)
		defer cancel()
		var err error
		frameID, err = p.waitMainFrame(waitCtx)
		if err != nil {
			return ExecutionTarget{}, fmt.Errorf("wait for main frame: %w", err)
		}
	}
	if frameID == "" {
		return ExecutionTarget{}, errors.New("main frame id is empty")
	}
	return p.runtimeTargetForNamespaceFrame(ctx, namespace, frameID)
}

func (p *Page) evalCoreRuntime(ctx context.Context, expression string) error {
	handle, err := p.Context(RuntimeContextOptions{Namespace: NamespaceIsolatedCore})
	if err != nil {
		return err
	}
	_, err = handle.eval(ctx, expression)
	return err
}
