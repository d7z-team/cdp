package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gopkg.d7z.net/cdp/internal/binding"
	engine "gopkg.d7z.net/cdp/internal/engine"
)

type Initializer struct {
	browser *Browser
	manager *engine.BrowserManager
}
type BindingCall struct {
	Page                                           *Page
	Name, Payload, SessionID, TargetID, TargetType string
	ExecutionContextID                             int
}
type Binding struct {
	Name   string
	World  World
	Handle func(context.Context, BindingCall) error
}
type bindingAdapter struct {
	browser *Browser
	spec    Binding
}

func (b bindingAdapter) Name() string { return b.spec.Name }
func (b bindingAdapter) Handle(ctx engine.BindingContext, event *binding.BindingCalledEvent) error {
	return b.spec.Handle(ctx.Context, BindingCall{Page: b.browser.wrap(ctx.Page), Name: event.Name, Payload: event.Payload, SessionID: ctx.SessionID, TargetID: ctx.TargetID, TargetType: ctx.TargetType, ExecutionContextID: ctx.ExecutionContextID})
}
func (i *Initializer) RegisterBinding(spec Binding) error {
	if i == nil || i.manager == nil {
		return ErrClosed
	}
	if err := spec.World.validate(); err != nil {
		return err
	}
	if spec.Name == "" || spec.Handle == nil {
		return errors.New("binding requires name and handler")
	}
	if spec.World == "" {
		spec.World = WorldCore
	}
	return i.manager.RegisterBindingInNamespace(engine.RuntimeNamespace(spec.World), bindingAdapter{browser: i.browser, spec: spec})
}

// InitScript.Source is a JavaScript function body. Probe is a boolean expression.
// Cleanup, when provided, should invoke only the facade destroy method.
type InitScript struct {
	Name                          string
	World                         World
	Source, Probe, Cleanup, Label string
}

func (i *Initializer) RegisterInitScript(s InitScript) error {
	if i == nil || i.manager == nil {
		return ErrClosed
	}
	if err := s.World.validate(); err != nil {
		return err
	}
	if s.World == "" {
		s.World = WorldCore
	}
	return i.manager.RegisterInitScript(engine.InitScript{Name: s.Name, Namespace: engine.RuntimeNamespace(s.World), Exec: "(async () => {\n" + s.Source + "\n})()", Probe: s.Probe, Cleanup: s.Cleanup, Label: s.Label})
}

type LifecycleEvent struct {
	Type, Source                                                                                string
	Timestamp                                                                                   time.Time
	Sequence                                                                                    uint64
	PageID, OpenerPageID, OpenKind, Title, URL, PreviousPageID, PreviousURL, Reason, ScriptName string
	World                                                                                       World
	Generation                                                                                  int64
	TargetInfo                                                                                  json.RawMessage
}
type lifecycleAdapter struct {
	name    string
	handler func(context.Context, LifecycleEvent) error
}

func (l lifecycleAdapter) Name() string { return l.name }
func (l lifecycleAdapter) HandleLifecycle(ctx engine.LifecycleContext, e engine.LifecycleEvent) error {
	raw, _ := json.Marshal(e.TargetInfo)
	return l.handler(ctx.Context, LifecycleEvent{Type: string(e.Type), Source: string(e.Source), Timestamp: e.Timestamp, Sequence: e.Sequence, PageID: e.PageID, OpenerPageID: e.OpenerPageID, OpenKind: string(e.OpenKind), Title: e.Title, URL: e.URL, PreviousPageID: e.PreviousPageID, PreviousURL: e.PreviousURL, Reason: e.Reason, ScriptName: e.ScriptName, World: World(e.Namespace), Generation: e.Generation, TargetInfo: raw})
}
func (i *Initializer) RegisterLifecycleHandler(name string, h func(context.Context, LifecycleEvent) error) error {
	if i == nil || i.manager == nil {
		return ErrClosed
	}
	if name == "" || h == nil {
		return errors.New("lifecycle requires name and handler")
	}
	return i.manager.RegisterLifecycle(lifecycleAdapter{name: name, handler: h})
}
