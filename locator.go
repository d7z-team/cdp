package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	engine "gopkg.d7z.net/cdp/internal/engine"
)

type Locator struct {
	page *Page
	root *Element
	plan engine.SelectorPlan
}

func (p *Page) Locator(selectors ...string) *Locator {
	return (&Locator{page: p}).Locator(selectors...)
}
func (l *Locator) append(layer engine.SelectorPlanLayer) *Locator {
	if l == nil {
		l = &Locator{}
	}
	n := &Locator{page: l.page, root: l.root, plan: l.plan}
	n.plan.Layers = append([]engine.SelectorPlanLayer(nil), l.plan.Layers...)
	if n.plan.Terminal != nil {
		n.plan.Layers = append(n.plan.Layers, engine.SelectorPlanLayer{Filter: n.plan.Terminal})
		n.plan.Terminal = nil
	}
	n.plan.Layers = append(n.plan.Layers, layer)
	return n
}
func (l *Locator) Locator(selectors ...string) *Locator {
	var options []engine.SelectorPlanOption
	for _, s := range selectors {
		if s = strings.TrimSpace(s); s != "" {
			options = append(options, engine.SelectorPlanOption{Kind: engine.SelectorPlanOptionKindSelector, Raw: s})
		}
	}
	return l.append(engine.SelectorPlanLayer{Options: options})
}
func (l *Locator) Nth(index int) *Locator {
	if l == nil {
		l = &Locator{}
	}
	n := &Locator{page: l.page, root: l.root, plan: l.plan}
	n.plan.Layers = append([]engine.SelectorPlanLayer(nil), l.plan.Layers...)
	if n.plan.Terminal != nil {
		n.plan.Layers = append(n.plan.Layers, engine.SelectorPlanLayer{Filter: n.plan.Terminal})
	}
	n.plan.Terminal = &engine.SelectorPlanTerminal{Kind: engine.SelectorPlanTerminalKindNth, Index: index}
	return n
}
func (l *Locator) First() *Locator { return l.Nth(0) }
func (l *Locator) Last() *Locator {
	n := l.Nth(0)
	n.plan.Terminal = &engine.SelectorPlanTerminal{Kind: engine.SelectorPlanTerminalKindLast}
	return n
}
func (l *Locator) ContentFrame() *Locator {
	return l.append(engine.SelectorPlanLayer{Options: []engine.SelectorPlanOption{{Kind: engine.SelectorPlanOptionKindFrameEnter}}})
}
func (l *Locator) FrameLocator(s string) *Locator { return l.Locator(s).ContentFrame() }
func (p *Page) FrameLocator(s string) *Locator    { return p.Locator(s).ContentFrame() }

type TextOptions struct{ Exact bool }
type RoleOptions struct{ Name string }

func quote(s string) string { v, _ := json.Marshal(s); return string(v) }
func (l *Locator) ByRole(role string, opts RoleOptions) *Locator {
	s := "role=" + role
	if opts.Name != "" {
		s += "[name=" + quote(opts.Name) + "]"
	}
	return l.Locator(s)
}
func (l *Locator) ByText(text string, opts TextOptions) *Locator {
	if opts.Exact {
		return l.Locator("text-is=" + quote(text))
	}
	return l.Locator("text=" + quote(text))
}
func (l *Locator) ByTestID(id string) *Locator { return l.Locator("[data-testid=" + quote(id) + "]") }
func (l *Locator) ByLabel(text string, opts TextOptions) *Locator {
	s := "label=" + quote(text)
	if opts.Exact {
		s = "label-is=" + quote(text)
	}
	return l.Locator(s)
}
func (l *Locator) ByPlaceholder(text string, opts TextOptions) *Locator {
	s := "placeholder=" + quote(text)
	if opts.Exact {
		s = "placeholder-is=" + quote(text)
	}
	return l.Locator(s)
}
func (p *Page) ByRole(role string, opts RoleOptions) *Locator {
	return (&Locator{page: p}).ByRole(role, opts)
}
func (p *Page) ByText(text string, opts TextOptions) *Locator {
	return (&Locator{page: p}).ByText(text, opts)
}
func (p *Page) ByTestID(id string) *Locator { return (&Locator{page: p}).ByTestID(id) }
func (p *Page) ByLabel(text string, opts TextOptions) *Locator {
	return (&Locator{page: p}).ByLabel(text, opts)
}
func (p *Page) ByPlaceholder(text string, opts TextOptions) *Locator {
	return (&Locator{page: p}).ByPlaceholder(text, opts)
}

type queryResult struct {
	ids, visible, actionable []engine.SelectorTargetRef
	count                    int
	failure                  error
}

func (l *Locator) query(ctx context.Context, purpose string, limit int) (queryResult, error) {
	if l == nil || l.page == nil {
		return queryResult{}, ErrClosed
	}
	if len(l.plan.Layers) == 0 {
		return queryResult{}, errors.New("empty locator")
	}
	mode := engine.SelectorResolveModeLoad
	if purpose != "" {
		mode = engine.SelectorResolveModeActionable
	}
	p := l.page.engine
	options := engine.SelectorQueryOptions{Mode: mode, ResultMode: engine.SelectorQueryResultModeFull, ActionMode: p.ActionMode(), ActionPurpose: purpose}
	var s engine.SelectorQuerySession
	var err error
	if l.root != nil {
		if err = l.root.validate(ctx); err != nil {
			return queryResult{}, err
		}
		s, err = p.StartSelectorQueryFromRef(ctx, l.root.ref, l.plan, options)
	} else {
		s, err = p.StartSelectorQuery(ctx, 0, l.plan, options)
	}
	if err != nil {
		return queryResult{}, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.timeouts().Shutdown)
		defer cancel()
		p.DisposeSelectorQuerySession(c, s)
	}()
	if s.Failure != nil && s.Failure.Kind == "syntax" {
		return queryResult{}, s.Failure.BrowserError()
	}
	ids, err := p.SelectorQueryRefs(ctx, 0, s, engine.SelectorQueryBucketIDs, limit)
	if err != nil {
		return queryResult{}, err
	}
	visible, err := p.SelectorQueryRefs(ctx, 0, s, engine.SelectorQueryBucketVisible, limit)
	if err != nil {
		return queryResult{}, err
	}
	actionable, err := p.SelectorQueryRefs(ctx, 0, s, engine.SelectorQueryBucketActionable, limit)
	var failure error
	if browserErr := s.Failure.BrowserError(); browserErr != nil {
		failure = browserErr
	}
	return queryResult{ids: ids, visible: visible, actionable: actionable, count: s.IDs.Count, failure: failure}, err
}
func (l *Locator) Count(ctx context.Context) (int, error) {
	c, cancel, err := l.operation(ctx, l.timeouts().Read)
	if err != nil {
		return 0, err
	}
	defer cancel()
	result, err := l.query(c, "", 1)
	return result.count, operationError("locator.count", err)
}
func (l *Locator) Exists(ctx context.Context) (bool, error) {
	n, err := l.Count(ctx)
	return n > 0, err
}
func (l *Locator) All(ctx context.Context) ([]*Element, error) {
	c, cancel, err := l.operation(ctx, l.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := l.query(c, "", 0)
	if err != nil {
		return nil, operationError("locator.all", err)
	}
	out := make([]*Element, 0, len(result.ids))
	for _, ref := range result.ids {
		out = append(out, l.page.element(ref))
	}
	return out, nil
}
func (l *Locator) resolve(ctx context.Context, purpose string) (*Element, error) {
	var result *Element
	var counts MatchCounts
	var last error
	repositioned := map[engine.SelectorTargetRef]bool{}
	err := poll(ctx, func() (bool, error) {
		query, err := l.query(ctx, purpose, 8)
		ids, visible, actionable, count := query.ids, query.visible, query.actionable, query.count
		if err == nil {
			counts = MatchCounts{IDs: count, Visible: len(visible), Actionable: len(actionable)}
		}
		if err != nil {
			return false, err
		}
		if purpose == "" {
			if len(ids) == 1 {
				result = l.page.element(ids[0])
				return true, nil
			}
			if len(visible) == 1 {
				result = l.page.element(visible[0])
				return true, nil
			}
			last = query.failure
			if last == nil {
				last = fmt.Errorf("%w: expected unique element, matched %d", ErrNotFound, len(ids))
			}
			return false, nil
		}
		if len(actionable) == 1 {
			result = l.page.element(actionable[0])
			return true, nil
		}
		if len(actionable) > 1 {
			last = fmt.Errorf("matched %d actionable elements", len(actionable))
			return false, nil
		}
		for _, ref := range ids {
			_, _ = l.page.engine.SelectorTargetVisible(ctx, ref)
		}
		if len(ids) == 1 {
			d, err := l.page.engine.SelectorTargetActionabilityDiagnosticForPurpose(ctx, ids[0], purpose)
			if err != nil {
				return false, err
			}
			if d.Kind == "obscured" && !repositioned[ids[0]] {
				repositioned[ids[0]] = true
				d, err = l.page.engine.SelectorTargetRepositionForAction(ctx, ids[0], purpose)
				if err != nil {
					return false, err
				}
			}
			if d.Actionable {
				result = l.page.element(ids[0])
				return true, nil
			}
			last = engine.BrowserErrorFromActionability("locator.actionability", d)
		} else {
			last = query.failure
			if last == nil {
				last = fmt.Errorf("%w: expected actionable element, matched %d", ErrNotFound, len(ids))
			}
		}
		return false, nil
	})
	if err != nil {
		raw, _ := json.Marshal(l.plan)
		return nil, &LocatorError{Op: "locator.resolve", Selector: string(raw), Counts: counts, Cause: operationError("locator.resolve", errors.Join(last, err))}
	}
	return result, nil
}

func (l *Locator) wrapError(op string, err error) error {
	if err == nil {
		return nil
	}
	var plan engine.SelectorPlan
	if l != nil {
		plan = l.plan
	}
	raw, _ := json.Marshal(plan)
	var existing *LocatorError
	counts := MatchCounts{}
	if errors.As(err, &existing) {
		counts = existing.Counts
	}
	return &LocatorError{Op: "locator." + op, Selector: string(raw), Counts: counts, Cause: operationError("locator."+op, err)}
}

func (l *Locator) timeouts() Timeouts {
	if l == nil {
		return (*Page)(nil).timeouts()
	}
	return l.page.timeouts()
}
func (l *Locator) operation(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if l == nil {
		return nil, nil, ErrClosed
	}
	return l.page.operation(ctx, timeout)
}
