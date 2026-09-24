package engine

//go:generate go run ../cmd/bindgen -type CallCore -go-binding CallCoreBinding -ts-class CallCore -go callgen_core.go -ts ../webassets/src/bindings/core.ts

type CallCore struct{}

//gocall:name coreRuntimeReady
func (CallCore) CoreRuntimeReady(ctx GoCallContext, top bool) {
	if !top || ctx.Page == nil || ctx.Manager == nil || ctx.SessionID != "" || !ctx.Manager.isCurrentManagedPage(ctx.Page) {
		return
	}
	if !ctx.Page.ConfirmTopFrameContext(ctx.ExecutionContextID) {
		return
	}
	ctx.Manager.ConfirmInitScriptRuntimeReady(ctx.Page, "core.js", ctx.ExecutionContextID)
}

//gocall:name onFocus
func (CallCore) OnFocus(ctx GoCallContext) error {
	ctx.Manager.setActivePage(ctx.Page.ID, LifecycleSourceHelper, "window_focus")
	return nil
}

//gocall:name pageId
func (CallCore) PageID(ctx GoCallContext) (string, error) {
	return ctx.Page.ID, nil
}

//gocall:name fullscreenState
func (CallCore) FullscreenState(ctx GoCallContext, active bool, element string) error {
	ctx.Page.setFullscreenState(active, element)
	return nil
}
