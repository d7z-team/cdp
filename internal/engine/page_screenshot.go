package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log/slog"
	"math"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/imageutil"
	"gopkg.d7z.net/cdp/internal/webassets"
)

const (
	screenshotFrameWait      = 3 * time.Second
	screenshotCleanupTimeout = 2 * time.Second
	screenshotMaxPixels      = 100_000_000
	screenshotMaxCSSArea     = 100_000_000
)

var screenshotScreencastOptions = ScreencastOptions{
	Format:        "png",
	Quality:       100,
	MaxWidth:      16384,
	MaxHeight:     16384,
	EveryNthFrame: 1,
}

type screenshotTile struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type screenshotTileResult struct {
	Source     Rect `json:"source"`
	Dest       Rect `json:"dest"`
	FreshFrame bool `json:"freshFrame,omitempty"`
}

type screenshotPlan struct {
	Token  string           `json:"token"`
	Width  float64          `json:"width"`
	Height float64          `json:"height"`
	Tiles  []screenshotTile `json:"tiles"`
}

type screenshotTargetInfo struct {
	TagName        string `json:"tagName"`
	IsDocumentRoot bool   `json:"isDocumentRoot"`
	InFrame        bool   `json:"inFrame"`
	ElementRect    Rect   `json:"elementRect"`
	FrameRect      Rect   `json:"frameRect"`
	Viewport       Rect   `json:"viewport"`
	DocumentSize   Rect   `json:"documentSize"`
}

type screenshotTargetError struct {
	Reason string
	Info   screenshotTargetInfo
}

func (e *screenshotTargetError) Error() string {
	if e == nil {
		return ""
	}
	info := e.Info
	return fmt.Sprintf(
		"invalid screenshot target: %s; target=%s frame=%v elementRect=%.2fx%.2f frameRect=%.2fx%.2f viewport=%.2fx%.2f document=%.2fx%.2f",
		e.Reason,
		info.TagName,
		info.InFrame,
		info.ElementRect.Width,
		info.ElementRect.Height,
		info.FrameRect.Width,
		info.FrameRect.Height,
		info.Viewport.Width,
		info.Viewport.Height,
		info.DocumentSize.Width,
		info.DocumentSize.Height,
	)
}

func (p *Page) Screenshot() (*image.RGBA, error) {
	return p.ScreenshotContext(p.ctx)
}

func (p *Page) ScreenshotContext(ctx context.Context) (*image.RGBA, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var result *image.RGBA
	err := p.runForegroundInteractionContext(ctx, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		result, err = p.capturePageScreenshot(ctx)
		return err
	})
	return result, err
}

func (p *Page) capturePageScreenshot(ctx context.Context) (*image.RGBA, error) {
	if err := p.checkConnContext(ctx); err != nil {
		return nil, err
	}
	runtimeTarget, err := p.topPageScreenshotRuntimeTarget(ctx)
	if err != nil {
		return nil, err
	}

	return p.captureWithScreenshotScreencast(ctx, func() (*image.RGBA, error) {
		overlayToken := p.suspendInjectedOverlaysBeforeScreenshot(ctx, runtimeTarget)
		defer func() {
			cleanupCtx, cancel := p.screenshotCleanupContext()
			defer cancel()
			p.restoreInjectedOverlaysAfterScreenshot(cleanupCtx, runtimeTarget, overlayToken)
		}()

		plan, err := p.beginPageScreenshotPlan(ctx, runtimeTarget)
		if err != nil {
			return nil, err
		}
		defer func() {
			cleanupCtx, cancel := p.screenshotCleanupContext()
			defer cancel()
			p.finishPageScreenshotPlan(cleanupCtx, runtimeTarget, plan.Token)
		}()

		return p.captureScreencastScreenshotPlan(ctx, plan, func(tile screenshotTile) (screenshotTileResult, error) {
			return p.applyPageScreenshotTile(ctx, runtimeTarget, plan.Token, tile)
		})
	})
}

func (p *Page) CaptureTargetScreenshotContext(ctx context.Context, ref SelectorTargetRef) (*image.RGBA, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var result *image.RGBA
	err := p.runForegroundInteractionContext(ctx, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		result, err = p.captureTargetScreenshot(ctx, ref)
		return err
	})
	return result, err
}

func (p *Page) captureTargetScreenshot(ctx context.Context, ref SelectorTargetRef) (*image.RGBA, error) {
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return nil, err
	}
	info, infoErr := p.screenshotTargetInfo(ctx, target, backendNodeID)
	if infoErr != nil {
		return nil, infoErr
	}
	if info.IsDocumentRoot && !info.InFrame {
		return p.capturePageScreenshot(ctx)
	}
	if !info.IsDocumentRoot && (info.ElementRect.Width <= 0 || info.ElementRect.Height <= 0) {
		return nil, &screenshotTargetError{Reason: "element rect is empty", Info: info}
	}

	if err := p.checkConnContext(ctx); err != nil {
		return nil, err
	}
	if info.IsDocumentRoot && info.InFrame {
		p.revealFrameOwnerForScreenshot(ctx, target)
	}
	if info.InFrame && !info.IsDocumentRoot {
		if rect, ok, err := p.selectorTargetRectContext(ctx, ref, true); err != nil {
			return nil, err
		} else if !ok || rect.Width <= 0 || rect.Height <= 0 {
			info.ElementRect = rect
			return nil, &screenshotTargetError{Reason: "element rect is empty", Info: info}
		}
	}
	overlayTarget := target
	if overlayTarget.ContextID == 0 {
		if topTarget, targetErr := p.topPageScreenshotRuntimeTarget(ctx); targetErr == nil {
			overlayTarget = topTarget
		}
	}
	return p.captureWithScreenshotScreencast(ctx, func() (*image.RGBA, error) {
		overlayToken := p.suspendInjectedOverlaysBeforeScreenshot(ctx, overlayTarget)
		defer func() {
			cleanupCtx, cancel := p.screenshotCleanupContext()
			defer cancel()
			p.restoreInjectedOverlaysAfterScreenshot(cleanupCtx, overlayTarget, overlayToken)
		}()

		runtimeMethods := elementScreenshotRuntimeMethods
		if info.IsDocumentRoot && info.InFrame {
			runtimeMethods = frameDocumentScreenshotRuntimeMethods
		}
		plan, err := p.beginBackendNodeScreenshotPlan(ctx, target, backendNodeID, runtimeMethods.begin)
		if err != nil {
			return nil, err
		}
		defer func() {
			cleanupCtx, cancel := p.screenshotCleanupContext()
			defer cancel()
			p.finishBackendNodeScreenshotPlan(cleanupCtx, target, backendNodeID, plan.Token, runtimeMethods.finish)
		}()

		return p.captureScreencastScreenshotPlan(ctx, plan, func(tile screenshotTile) (screenshotTileResult, error) {
			return p.applyBackendNodeScreenshotTile(ctx, target, backendNodeID, plan.Token, tile, runtimeMethods.apply)
		})
	})
}

func (p *Page) captureWithScreenshotScreencast(ctx context.Context, capture func() (*image.RGBA, error)) (*image.RGBA, error) {
	if err := p.EnsureScreencastFor(ctx, ScreencastOwnerScreenshot, screenshotScreencastOptions); err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := p.screenshotCleanupContext()
		defer cancel()
		if err := p.StopScreencastFor(cleanupCtx, ScreencastOwnerScreenshot); err != nil && !errors.Is(err, ErrBrowserClosed) {
			p.log(slog.LevelDebug, "stop screenshot screencast failed", "page_id", p.ID, "error", err)
		}
	}()
	return capture()
}

func (p *Page) screenshotCleanupContext() (context.Context, context.CancelFunc) {
	parent := p.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, screenshotCleanupTimeout)
}

func (p *Page) topPageScreenshotRuntimeTarget(ctx context.Context) (ExecutionTarget, error) {
	target, err := p.mainFrameRuntimeTarget(ctx, NamespaceIsolatedCore)
	if err != nil && strings.Contains(err.Error(), "main frame id is empty") {
		return ExecutionTarget{}, errors.New("top page screenshot runtime frame id is empty")
	}
	return target, err
}

func (p *Page) evalScreenshotRuntimeResult(ctx context.Context, target ExecutionTarget, expression string) (map[string]any, error) {
	return p.RuntimeEvaluateOnTarget(ctx, target, expression, true)
}

func (p *Page) evalScreenshotRuntimeString(ctx context.Context, target ExecutionTarget, expression string) (string, error) {
	result, err := p.evalScreenshotRuntimeResult(ctx, target, expression)
	if err != nil {
		return "", err
	}
	if value, ok := SafeGet[string](result, "result", "value"); ok {
		return value, nil
	}
	return "", fmt.Errorf("runtime result is not string: %v", result)
}

func (p *Page) suspendInjectedOverlaysBeforeScreenshot(ctx context.Context, target ExecutionTarget) string {
	token, err := p.evalScreenshotRuntimeString(ctx, target, webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI,
		"suspendInjectedOverlaysForScreenshot",
		"core screenshot helper is not available",
	))
	if err != nil {
		if !errors.Is(err, ErrBrowserClosed) {
			p.log(slog.LevelDebug, "suspend injected overlays before screenshot failed", "page_id", p.ID, "error", err)
		}
		return ""
	}
	return token
}

func (p *Page) restoreInjectedOverlaysAfterScreenshot(ctx context.Context, target ExecutionTarget, token string) {
	if token == "" {
		return
	}
	if _, err := p.evalScreenshotRuntimeResult(ctx, target, webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI,
		"restoreInjectedOverlaysForScreenshot",
		"core screenshot helper is not available",
		webassets.JSLit(token),
	)); err != nil && !errors.Is(err, ErrBrowserClosed) {
		p.log(slog.LevelDebug, "restore injected overlays after screenshot failed", "page_id", p.ID, "token", token, "error", err)
	}
}

func (p *Page) screenshotTargetInfo(ctx context.Context, target ExecutionTarget, backendNodeID int) (screenshotTargetInfo, error) {
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, `function() {
		const rectOf = (value) => value ? {
			x: Number(value.x || value.left || 0),
			y: Number(value.y || value.top || 0),
			width: Number(value.width || 0),
			height: Number(value.height || 0),
		} : {x: 0, y: 0, width: 0, height: 0};
		const maxSize = (axis) => {
			const root = document.documentElement;
			const body = document.body;
			const viewport = axis === 'x' ? (window.visualViewport?.width || window.innerWidth || root?.clientWidth || 0) : (window.visualViewport?.height || window.innerHeight || root?.clientHeight || 0);
			const values = axis === 'x'
				? [root?.scrollWidth || 0, root?.offsetWidth || 0, root?.clientWidth || 0, body?.scrollWidth || 0, body?.offsetWidth || 0, body?.clientWidth || 0, viewport]
				: [root?.scrollHeight || 0, root?.offsetHeight || 0, root?.clientHeight || 0, body?.scrollHeight || 0, body?.offsetHeight || 0, body?.clientHeight || 0, viewport];
			return Math.max(0, ...values.filter(Number.isFinite));
		};
		let frameRect = {x: 0, y: 0, width: 0, height: 0};
		try {
			if (window.frameElement) {
				frameRect = rectOf(window.frameElement.getBoundingClientRect());
			}
		} catch (_) {}
		return {
			tagName: this?.tagName ? String(this.tagName).toLowerCase() : '',
			isDocumentRoot: !!this && (this === document.body || this === document.documentElement),
			inFrame: window.parent !== window,
			elementRect: rectOf(this?.getBoundingClientRect?.()),
			frameRect,
			viewport: {x: 0, y: 0, width: window.visualViewport?.width || window.innerWidth || document.documentElement?.clientWidth || 0, height: window.visualViewport?.height || window.innerHeight || document.documentElement?.clientHeight || 0},
			documentSize: {x: 0, y: 0, width: maxSize('x'), height: maxSize('y')},
		};
	}`)
	if err != nil {
		return screenshotTargetInfo{}, err
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil {
		return screenshotTargetInfo{}, runtimeErr
	}
	return screenshotValueFromResult[screenshotTargetInfo](res)
}

func (p *Page) revealFrameOwnerForScreenshot(ctx context.Context, target ExecutionTarget) {
	frameID := strings.TrimSpace(target.FrameID)
	if frameID == "" {
		return
	}
	res, err := p.sendTargetMessage(ctx, topPageExecutionTarget(), "DOM.getFrameOwner", map[string]any{"frameId": frameID})
	if err != nil {
		p.log(slog.LevelDebug, "get frame owner for screenshot failed", "page_id", p.ID, "frame_id", frameID, "error", err)
		return
	}
	backendNodeID, _ := SafeGet[float64](res, "backendNodeId")
	nodeID, _ := SafeGet[float64](res, "nodeId")
	switch {
	case backendNodeID > 0:
		if err := p.sendTargetPacket(ctx, topPageExecutionTarget(), "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": int(backendNodeID)}); err != nil {
			p.log(slog.LevelDebug, "scroll frame owner into view for screenshot failed", "page_id", p.ID, "frame_id", frameID, "backend_node_id", int(backendNodeID), "error", err)
		}
	case nodeID > 0:
		if err := p.sendTargetPacket(ctx, topPageExecutionTarget(), "DOM.scrollIntoViewIfNeeded", map[string]any{"nodeId": int(nodeID)}); err != nil {
			p.log(slog.LevelDebug, "scroll frame owner node into view for screenshot failed", "page_id", p.ID, "frame_id", frameID, "node_id", int(nodeID), "error", err)
		}
	default:
		p.log(slog.LevelDebug, "frame owner for screenshot has no node id", "page_id", p.ID, "frame_id", frameID)
	}
}

func (p *Page) beginPageScreenshotPlan(ctx context.Context, target ExecutionTarget) (screenshotPlan, error) {
	res, err := p.evalScreenshotRuntimeResult(ctx, target, webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI,
		"beginPageScreenshot",
		"core screenshot helper is not available",
	))
	if err != nil {
		return screenshotPlan{}, err
	}
	return screenshotPlanFromResult(res)
}

func (p *Page) applyPageScreenshotTile(ctx context.Context, target ExecutionTarget, token string, tile screenshotTile) (screenshotTileResult, error) {
	res, err := p.evalScreenshotRuntimeResult(ctx, target, webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI,
		"applyPageScreenshotTile",
		"core screenshot helper is not available",
		webassets.JSLit(token),
		webassets.JSLit(tile),
	))
	if err != nil {
		return screenshotTileResult{}, err
	}
	return screenshotTileResultFromResult(res)
}

func ffiElementRequiredFunction(params string, method string, missingMessage string, args ...webassets.JSArg) string {
	callArgs := append([]webassets.JSArg{webassets.JSRaw("this")}, args...)
	return "function(" + params + ") { return " + webassets.RuntimeRequiredMethodCall(
		webassets.RuntimeFFI,
		method,
		missingMessage,
		callArgs...,
	) + "; }"
}

func ffiOptionalFunction(params string, method string, args ...webassets.JSArg) string {
	return "function(" + params + ") { return " + webassets.RuntimeOptionalMethodCall(
		webassets.RuntimeFFI,
		method,
		args...,
	) + "; }"
}

func (p *Page) finishPageScreenshotPlan(ctx context.Context, target ExecutionTarget, token string) {
	if token == "" {
		return
	}
	if _, err := p.evalScreenshotRuntimeResult(ctx, target, webassets.RuntimeOptionalMethodCall(
		webassets.RuntimeFFI,
		"finishPageScreenshot",
		webassets.JSLit(token),
	)); err != nil && !errors.Is(err, ErrBrowserClosed) {
		p.log(slog.LevelDebug, "finish page screenshot plan failed", "page_id", p.ID, "token", token, "error", err)
	}
}

type backendNodeScreenshotRuntimeMethods struct {
	begin  string
	apply  string
	finish string
}

var (
	elementScreenshotRuntimeMethods = backendNodeScreenshotRuntimeMethods{
		begin: "beginElementScreenshot", apply: "applyElementScreenshotTile", finish: "finishElementScreenshot",
	}
	frameDocumentScreenshotRuntimeMethods = backendNodeScreenshotRuntimeMethods{
		begin: "beginFrameDocumentScreenshot", apply: "applyFrameDocumentScreenshotTile", finish: "finishFrameDocumentScreenshot",
	}
)

func (p *Page) beginBackendNodeScreenshotPlan(ctx context.Context, target ExecutionTarget, backendNodeID int, method string) (screenshotPlan, error) {
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, ffiElementRequiredFunction(
		"",
		method,
		"core screenshot helper is not available",
	))
	if err != nil {
		return screenshotPlan{}, err
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil {
		return screenshotPlan{}, runtimeErr
	}
	return screenshotPlanFromResult(res)
}

func (p *Page) applyBackendNodeScreenshotTile(ctx context.Context, target ExecutionTarget, backendNodeID int, token string, tile screenshotTile, method string) (screenshotTileResult, error) {
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, ffiElementRequiredFunction(
		"token, tile",
		method,
		"core screenshot helper is not available",
		webassets.JSRaw("token"),
		webassets.JSRaw("tile"),
	), map[string]any{"value": token}, map[string]any{"value": tile})
	if err != nil {
		return screenshotTileResult{}, err
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil {
		return screenshotTileResult{}, runtimeErr
	}
	return screenshotTileResultFromResult(res)
}

func (p *Page) finishBackendNodeScreenshotPlan(ctx context.Context, target ExecutionTarget, backendNodeID int, token string, method string) {
	if token == "" || backendNodeID <= 0 {
		return
	}
	res, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, ffiOptionalFunction(
		"token",
		method,
		webassets.JSRaw("token"),
	), map[string]any{"value": token})
	if err != nil {
		if !errors.Is(err, ErrBrowserClosed) {
			p.log(slog.LevelDebug, "finish backend node screenshot plan failed", "page_id", p.ID, "method", method, "token", token, "error", err)
		}
		return
	}
	if runtimeErr := runtimeResultError(res); runtimeErr != nil && !errors.Is(runtimeErr, ErrBrowserClosed) {
		p.log(slog.LevelDebug, "finish backend node screenshot plan failed", "page_id", p.ID, "method", method, "token", token, "error", runtimeErr)
	}
}

func (p *Page) captureScreencastScreenshotPlan(ctx context.Context, plan screenshotPlan, applyTile func(screenshotTile) (screenshotTileResult, error)) (*image.RGBA, error) {
	if err := validateScreenshotPlan(plan); err != nil {
		return nil, err
	}
	var (
		output       *image.RGBA
		outputWidth  int
		outputHeight int
		scaleX       float64
		scaleY       float64
	)

	for index, tile := range plan.Tiles {
		if tile.Width <= 0 || tile.Height <= 0 {
			continue
		}
		sequence := p.screencastSequence()
		result, err := applyTile(tile)
		if err != nil {
			return nil, err
		}
		if result.Source.Width <= 0 || result.Source.Height <= 0 || result.Dest.Width <= 0 || result.Dest.Height <= 0 {
			continue
		}
		frameCtx, cancel := context.WithTimeout(ctx, screenshotFrameWait)
		capture, err := p.CaptureScreencastCropImageAfterContext(frameCtx, result.Source, ScreencastCropOptions{}, sequence, !result.FreshFrame)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("screenshot tile %d/%d frame: %w", index+1, len(plan.Tiles), err)
		}
		if capture == nil || capture.Bounds().Dx() <= 0 || capture.Bounds().Dy() <= 0 {
			continue
		}
		if output == nil {
			scaleX = float64(capture.Bounds().Dx()) / result.Source.Width
			scaleY = float64(capture.Bounds().Dy()) / result.Source.Height
			outputWidth = int(math.Round(plan.Width * scaleX))
			outputHeight = int(math.Round(plan.Height * scaleY))
			if outputWidth <= 0 || outputHeight <= 0 {
				return nil, fmt.Errorf("invalid screenshot output size: %dx%d", outputWidth, outputHeight)
			}
			if int64(outputWidth)*int64(outputHeight) > screenshotMaxPixels {
				return nil, fmt.Errorf("screenshot output too large: %dx%d", outputWidth, outputHeight)
			}
			output = image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))
		}
		destX := int(math.Round(result.Dest.X * scaleX))
		destY := int(math.Round(result.Dest.Y * scaleY))
		destRight := int(math.Round((result.Dest.X + result.Dest.Width) * scaleX))
		destBottom := int(math.Round((result.Dest.Y + result.Dest.Height) * scaleY))
		remainingRight := plan.Width - (result.Dest.X + result.Dest.Width)
		remainingBottom := plan.Height - (result.Dest.Y + result.Dest.Height)
		if remainingRight >= -0.01 && remainingRight <= 1 {
			destRight = outputWidth
		}
		if remainingBottom >= -0.01 && remainingBottom <= 1 {
			destBottom = outputHeight
		}
		targetWidth := destRight - destX
		targetHeight := destBottom - destY
		if targetWidth <= 0 || targetHeight <= 0 {
			continue
		}
		if capture.Bounds().Dx() != targetWidth || capture.Bounds().Dy() != targetHeight {
			resized, resizeErr := imageutil.Resize(capture, targetWidth, targetHeight)
			if resizeErr != nil {
				return nil, fmt.Errorf("resize screenshot tile: %w", resizeErr)
			}
			capture = resized
		}
		draw.Draw(output, capture.Bounds().Add(image.Pt(destX, destY)), capture, capture.Bounds().Min, draw.Over)
	}
	if output == nil {
		return nil, errors.New("未能获取有效截图内容")
	}
	return output, nil
}

func validateScreenshotPlan(plan screenshotPlan) error {
	if plan.Token == "" {
		return errors.New("截图计划缺少 token")
	}
	if plan.Width <= 0 || plan.Height <= 0 || math.IsNaN(plan.Width) || math.IsNaN(plan.Height) || math.IsInf(plan.Width, 0) || math.IsInf(plan.Height, 0) {
		return fmt.Errorf("无效的截图尺寸: %.2fx%.2f", plan.Width, plan.Height)
	}
	if plan.Width*plan.Height > screenshotMaxCSSArea {
		return fmt.Errorf("截图区域过大: %.0fx%.0f", plan.Width, plan.Height)
	}
	if len(plan.Tiles) == 0 {
		return errors.New("截图计划没有可采集分块")
	}
	return nil
}

func screenshotPlanFromResult(res map[string]any) (screenshotPlan, error) {
	return screenshotValueFromResult[screenshotPlan](res)
}

func screenshotTileResultFromResult(res map[string]any) (screenshotTileResult, error) {
	return screenshotValueFromResult[screenshotTileResult](res)
}

func screenshotValueFromResult[T any](res map[string]any) (T, error) {
	var zero T
	value, ok := SafeGet[map[string]any](res, "result", "value")
	if !ok {
		return zero, fmt.Errorf("截图 helper 返回无效结果: %v", res)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return zero, err
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, err
	}
	return result, nil
}
