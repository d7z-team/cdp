package engine

import (
	"context"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"gopkg.d7z.net/cdp/internal/webassets"
)

func (p *Page) rememberMousePosition(x, y float64) {
	p.lock.Lock()
	p.mouseX = x
	p.mouseY = y
	p.lock.Unlock()
}

// getViewportSize 返回当前页面的逻辑视口尺寸（CSS 像素）。
func (p *Page) getViewportSize(ctx context.Context) (float64, float64) {
	vps, err := p.EvalString("window.innerWidth+','+window.innerHeight")
	if err != nil {
		return 1920, 1080
	}
	parts := strings.Split(vps, ",")
	if len(parts) != 2 {
		return 1920, 1080
	}
	w, _ := strconv.ParseFloat(parts[0], 64)
	h, _ := strconv.ParseFloat(parts[1], 64)
	if w <= 0 {
		w = 1920
	}
	if h <= 0 {
		h = 1080
	}
	return w, h
}

// minJerk5 计算 5 阶最小 jerk 轨迹的归一化位置因子。
// Flash & Hogan (1985): x(t) = x₀ + Δx·(10τ³−15τ⁴+6τ⁵)
// 该曲线保证位置/速度/加速度在起止点均为零，速度呈钟形。
func minJerk5(tau float64) float64 {
	tau2 := tau * tau
	tau3 := tau2 * tau
	tau4 := tau3 * tau
	tau5 := tau4 * tau
	return 10*tau3 - 15*tau4 + 6*tau5
}

type humanMouseStep struct {
	x, y  float64
	delay time.Duration
}

type mouseVisualAction struct {
	Kind   string     `json:"kind"`
	X      float64    `json:"x"`
	Y      float64    `json:"y"`
	FromX  float64    `json:"fromX"`
	FromY  float64    `json:"fromY"`
	ToX    float64    `json:"toX"`
	ToY    float64    `json:"toY"`
	Button string     `json:"button,omitempty"`
	Mode   ActionMode `json:"mode"`
}

type mouseMovePlan struct {
	fromX, fromY float64
	toX, toY     float64
	steps        []humanMouseStep
}

const selfJitter = 1.8

const (
	minMouseStepDelay     = 900 * time.Microsecond
	maxMouseStepDelay     = 4 * time.Millisecond
	maxMouseMoveTotal     = 400 * time.Millisecond
	minMouseMotionBudget  = 32 * time.Millisecond
	maxMouseMotionBudget  = 160 * time.Millisecond
	maxMouseReactionDelay = 12 * time.Millisecond
)

func allocateMousePhaseDurations(count int, total time.Duration, tailBias float64) []time.Duration {
	if count <= 0 {
		return nil
	}
	if total <= 0 {
		delays := make([]time.Duration, count)
		for i := range delays {
			delays[i] = minMouseStepDelay
		}
		return delays
	}
	weights := make([]float64, count)
	var sum float64
	for i := range weights {
		tau := float64(i+1) / float64(count)
		weight := 0.9 + tailBias*tau*0.35
		weights[i] = weight
		sum += weight
	}
	delays := make([]time.Duration, count)
	remaining := total
	for i, weight := range weights {
		if i == count-1 {
			delays[i] = min(max(remaining, minMouseStepDelay), maxMouseStepDelay)
			break
		}
		delay := time.Duration(float64(total) * (weight / sum))
		if delay < minMouseStepDelay {
			delay = minMouseStepDelay
		}
		if delay > maxMouseStepDelay {
			delay = maxMouseStepDelay
		}
		delays[i] = delay
		if remaining > delay {
			remaining -= delay
		} else {
			remaining = minMouseStepDelay
		}
	}
	overflow := time.Duration(0)
	for _, delay := range delays {
		overflow += delay
	}
	overflow -= total
	for i := len(delays) - 1; i >= 0 && overflow > 0; i-- {
		reducible := delays[i] - minMouseStepDelay
		if reducible <= 0 {
			continue
		}
		if reducible > overflow {
			reducible = overflow
		}
		delays[i] -= reducible
		overflow -= reducible
	}
	return delays
}

func clampDuration(value, minValue, maxValue time.Duration) time.Duration {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func targetMouseStepCount(distance float64) int {
	switch {
	case distance < 120:
		return 7 + rand.IntN(4)
	case distance < 650:
		return 10 + rand.IntN(5)
	case distance < 900:
		return 14 + rand.IntN(7)
	default:
		return 18 + rand.IntN(7)
	}
}

// buildHumanMouseMoveSteps 生成完整的前馈-反馈轨迹步序列（纯计算，不发送 CDP 事件）。
// 返回 nil 表示本次生成失败（via 点或目标抖动超出视口）。
func (p *Page) buildHumanMouseMoveSteps(fromX, fromY, toX, toY, vpW, vpH float64) []humanMouseStep {
	dx := toX - fromX
	dy := toY - fromY
	distance := math.Sqrt(dx*dx + dy*dy)
	if distance < 5 {
		return []humanMouseStep{{toX, toY, 0}}
	}

	baseAngle := math.Atan2(dy, dx)

	// 初始运动方向随机化：保留距离衰减，但把随机性更多放到中段主曲率，而不是高频抖动。
	angleJitter := (0.52 - distance/3200) * (rand.Float64() - 0.5)
	if math.Abs(angleJitter) < 0.05 {
		angleJitter = 0.05 * (rand.Float64() - 0.5)
	}
	if math.Abs(angleJitter) > 0.24 {
		angleJitter = math.Copysign(0.24, angleJitter)
	}

	// 弹道阶段占比和方向误差
	ballisticRatio := 0.68 + rand.Float64()*0.12
	ballisticError := (rand.Float64()*0.42 - 0.21)
	ballisticAngle := baseAngle + angleJitter + ballisticError

	ballisticDist := distance * ballisticRatio
	viaX := fromX + ballisticDist*math.Cos(ballisticAngle)
	viaY := fromY + ballisticDist*math.Sin(ballisticAngle)

	// via 点超出视口则本次生成无效
	if viaX < 0 || viaX > vpW || viaY < 0 || viaY > vpH {
		return nil
	}

	// 保留原有两阶段几何，但直接降低有效采样点数，避免密集点列看起来发颤。
	totalSteps := targetMouseStepCount(distance)
	ballisticSteps := int(float64(totalSteps) * ballisticRatio)
	if ballisticSteps < 4 {
		ballisticSteps = 4
	}
	correctiveSteps := totalSteps - ballisticSteps
	if correctiveSteps < 3 {
		correctiveSteps = 3
		ballisticSteps = totalSteps - correctiveSteps
	}
	motionBudget := time.Duration(24+distance*0.075+rand.Float64()*8) * time.Millisecond
	motionBudget = clampDuration(motionBudget, minMouseMotionBudget, maxMouseMotionBudget)

	// 生理性震颤随机初始相位
	tremorPhase := rand.Float64() * 2 * math.Pi

	steps := make([]humanMouseStep, 0, totalSteps+1)
	pauseBudget := time.Duration(0)

	// 中段视觉采样停顿
	if rand.Float64() < 0.12 && ballisticSteps > 4 {
		pauseBudget = time.Duration(4+rand.IntN(7)) * time.Millisecond
		steps = append(steps, humanMouseStep{viaX, viaY, pauseBudget})
	}
	if motionBudget <= pauseBudget {
		motionBudget = clampDuration(pauseBudget+24*time.Millisecond, minMouseMotionBudget, maxMouseMotionBudget)
	}
	ballisticBudgetRatio := 0.66 + rand.Float64()*0.07
	ballisticBudget := time.Duration(float64(motionBudget-pauseBudget) * ballisticBudgetRatio)
	correctiveBudget := motionBudget - pauseBudget - ballisticBudget
	if correctiveBudget < 6*time.Millisecond {
		correctiveBudget = 6 * time.Millisecond
		if ballisticBudget > correctiveBudget {
			ballisticBudget = motionBudget - pauseBudget - correctiveBudget
		}
	}
	ballisticDelays := allocateMousePhaseDurations(ballisticSteps, ballisticBudget, 0.10)
	correctiveDelays := allocateMousePhaseDurations(correctiveSteps, correctiveBudget, 0.14)

	// 弹道阶段
	for i := 1; i <= ballisticSteps; i++ {
		tau := float64(i) / float64(ballisticSteps)
		mj := minJerk5(tau)

		x := fromX + (viaX-fromX)*mj
		y := fromY + (viaY-fromY)*mj

		tremorPhase += 0.65
		x += math.Sin(tremorPhase)*0.18*(0.5+rand.Float64()*0.5) + (rand.Float64()*0.7 - 0.35) + (rand.Float64()-0.5)*0.01
		y += math.Cos(tremorPhase)*0.18*(0.5+rand.Float64()*0.5) + (rand.Float64()*0.7 - 0.35) + (rand.Float64()-0.5)*0.01

		if x < 0 || x > vpW || y < 0 || y > vpH {
			return nil
		}
		steps = append(steps, humanMouseStep{x, y, ballisticDelays[i-1]})
	}

	// 修正阶段：二次贝塞尔弧线，起点切线对齐弹道到达方向，平滑转弯到目标
	corrDx := toX - viaX
	corrDy := toY - viaY
	corrDist := math.Sqrt(corrDx*corrDx + corrDy*corrDy)
	cpX := viaX + math.Cos(ballisticAngle)*corrDist*0.62
	cpY := viaY + math.Sin(ballisticAngle)*corrDist*0.62

	for i := 1; i <= correctiveSteps; i++ {
		tau := float64(i) / float64(correctiveSteps)
		u := minJerk5(tau)
		v := 1 - u

		x := v*v*viaX + 2*v*u*cpX + u*u*toX
		y := v*v*viaY + 2*v*u*cpY + u*u*toY

		tremorPhase += 0.55
		x += math.Sin(tremorPhase)*0.14*(0.5+rand.Float64()*0.4) + (rand.Float64()*0.5 - 0.25) + (rand.Float64()-0.5)*0.01
		y += math.Cos(tremorPhase)*0.14*(0.5+rand.Float64()*0.4) + (rand.Float64()*0.5 - 0.25) + (rand.Float64()-0.5)*0.01

		if x < 0 || x > vpW || y < 0 || y > vpH {
			return nil
		}
		steps = append(steps, humanMouseStep{x, y, correctiveDelays[i-1]})
	}
	return steps
}

func (p *Page) buildMouseMovePlanFrom(ctx context.Context,

	fromX, fromY, toX, toY float64) mouseMovePlan {
	dx := toX - fromX
	dy := toY - fromY
	distance := math.Sqrt(dx*dx + dy*dy)
	plan := mouseMovePlan{fromX: fromX, fromY: fromY, toX: toX, toY: toY}
	if distance < 5 {
		jx := toX + (rand.Float64()-0.5)*selfJitter
		jy := toY + (rand.Float64()-0.5)*selfJitter
		plan.steps = make([]humanMouseStep, 0, 3)
		for i := 0; i < 3; i++ {
			stepX := fromX + (jx-fromX)*float64(i+1)/3 + (rand.Float64()-0.5)*selfJitter
			stepY := fromY + (jy-fromY)*float64(i+1)/3 + (rand.Float64()-0.5)*selfJitter
			plan.steps = append(plan.steps, humanMouseStep{stepX, stepY, 2 * time.Millisecond})
		}
		return plan
	}

	vpW, vpH := p.getViewportSize(ctx)

	for attempt := 0; attempt < 5; attempt++ {
		if s := p.buildHumanMouseMoveSteps(fromX, fromY, toX, toY, vpW, vpH); s != nil {
			plan.steps = s
			return plan
		}
	}

	n := targetMouseStepCount(distance)
	plan.steps = make([]humanMouseStep, n)
	for i := range plan.steps {
		t := float64(i+1) / float64(n)
		x := fromX + dx*t + (rand.Float64()-0.5)*0.02
		y := fromY + dy*t + (rand.Float64()-0.5)*0.02
		if x < 0 {
			x = 0
		}
		if x > vpW {
			x = vpW
		}
		if y < 0 {
			y = 0
		}
		if y > vpH {
			y = vpH
		}
		plan.steps[i] = humanMouseStep{x, y, minMouseStepDelay}
	}
	return plan
}

func (p *Page) executeMouseMovePlan(ctx context.Context,

	plan mouseMovePlan) error {
	dx := plan.toX - plan.fromX
	dy := plan.toY - plan.fromY
	if math.Sqrt(dx*dx+dy*dy) >= 5 {
		if err := waitInputDelay(ctx, time.Duration(3+rand.IntN(6))*time.Millisecond); err != nil {
			return err
		}
	}
	for _, s := range plan.steps {
		if s.x == plan.fromX && s.y == plan.fromY && s.delay > 0 {
			// 中段停顿：不发送移动事件，只等待
			if err := waitInputDelay(ctx, s.delay); err != nil {
				return err
			}
			continue
		}
		if err := p.inputDispatchMouseEvent(ctx,
			"mouseMoved", s.x, s.y, "none", 0); err != nil {
			return err
		}
		p.rememberMousePosition(s.x, s.y)
		if err := waitInputDelay(ctx, s.delay); err != nil {
			return err
		}
	}
	return nil
}

func (p *Page) showMouseAction(ctx context.Context,

	action mouseVisualAction) {
	action.Mode = p.ActionMode()
	raw, err := json.Marshal(action)
	if err != nil {
		return
	}
	_ = p.evalCoreRuntime(ctx, webassets.RuntimeOptionalMethodCall(webassets.RuntimeFFI, "showMouseAction", webassets.JSRaw(string(raw))))
}

func (p *Page) showActionTarget(ctx context.Context,

	boxes []Rect) {
	if p.fastActionMode() || len(boxes) == 0 {
		return
	}
	raw, err := json.Marshal(boxes)
	if err != nil {
		return
	}
	_ = p.evalCoreRuntime(ctx, webassets.RuntimeOptionalMethodCall(
		webassets.RuntimeFFI,
		"canvas.drawMultiple",
		webassets.JSRaw(string(raw)),
		webassets.JSRaw(`{ owner: 'action-preview', color: '#ff0000', timeoutMs: 1200 }`),
	))
}

func (p *Page) pointTargetPreviewBoxes(ctx context.Context,

	x, y float64) []Rect {
	raw, err := p.EvalString(`(() => {
		const el = document.elementFromPoint(%f, %f);
		if (!el || !(el instanceof Element)) return '';
		const rect = el.getBoundingClientRect();
		if (!rect || rect.width <= 0 || rect.height <= 0) return '';
		return JSON.stringify({x: rect.left, y: rect.top, width: rect.width, height: rect.height});
	})()`, x, y)
	if err != nil || raw == "" {
		return nil
	}
	var rect Rect
	if err := json.Unmarshal([]byte(raw), &rect); err != nil {
		return nil
	}
	if rect.Width <= 0 || rect.Height <= 0 {
		return nil
	}
	return []Rect{rect}
}

// executeHumanMouseMove 模拟人类鼠标运动轨迹。
// 基于前馈-反馈双层控制的叠加模型：
//
//	前馈弹道阶段（开环）→ 反馈修正阶段（闭环）+ 生理性震颤叠加。
func (p *Page) executeHumanMouseMove(ctx context.Context,

	fromX, fromY, toX, toY float64) error {
	return p.executeMouseMovePlan(ctx,
		p.buildMouseMovePlanFrom(ctx,
			fromX, fromY, toX, toY))
}

// MouseMove 移动鼠标到指定坐标。
// 若没有缓存位置，则在 viewport 内选取一个执行轨迹起点。
func (p *Page) MouseMove(x, y float64) error {
	return p.MouseMoveContext(p.ctx, x, y)
}

func (p *Page) MouseMoveContext(ctx context.Context, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		p.showMouseAction(ctx,
			mouseVisualAction{Kind: "move", X: x, Y: y})
		return p.mouseMove(ctx,
			x, y)
	})
}

func (p *Page) mouseMove(ctx context.Context,

	x, y float64) error {
	if p.fastActionMode() {
		if err := p.inputDispatchMouseEvent(ctx,
			"mouseMoved", x, y, "none", 0); err != nil {
			return err
		}
		p.rememberMousePosition(x, y)
		return nil
	}

	p.lock.RLock()
	fromX, fromY := p.mouseX, p.mouseY
	p.lock.RUnlock()

	if fromX == 0 && fromY == 0 {
		vpW, vpH := p.getViewportSize(ctx)
		fromX = rand.Float64()*vpW*0.7 + vpW*0.15
		fromY = rand.Float64()*vpH*0.7 + vpH*0.15
		p.rememberMousePosition(fromX, fromY)
	}
	return p.executeHumanMouseMove(ctx,
		fromX, fromY, x, y)
}

// MouseDown 在指定坐标按下鼠标按键
func (p *Page) MouseDown(ctx context.Context,

	x, y float64, button string, clickCount int) error {
	return p.runForegroundInteractionContext(ctx,
		func() error {
			p.showMouseAction(ctx,
				mouseVisualAction{Kind: "press", X: x, Y: y, Button: button})
			return p.mouseDown(ctx,
				x, y, button, clickCount)
		})
}

func (p *Page) mouseDown(ctx context.Context,

	x, y float64, button string, clickCount int) error {
	err := p.inputDispatchMouseEvent(ctx,
		"mousePressed", x, y, button, clickCount)
	if err == nil {
		p.rememberMousePosition(x, y)
	}
	return err
}

// MouseUp 在指定坐标释放鼠标按键
func (p *Page) MouseUp(ctx context.Context,

	x, y float64, button string, clickCount int) error {
	return p.runForegroundInteractionContext(ctx,
		func() error {
			p.showMouseAction(ctx,
				mouseVisualAction{Kind: "release", X: x, Y: y, Button: button})
			return p.mouseUp(ctx,
				x, y, button, clickCount)
		})
}

func (p *Page) mouseUp(ctx context.Context,

	x, y float64, button string, clickCount int) error {
	err := p.inputDispatchMouseEvent(ctx,
		"mouseReleased", x, y, button, clickCount)
	if err == nil {
		p.rememberMousePosition(x, y)
	}
	return err
}

func (p *Page) dispatchMouseClick(ctx context.Context,

	x, y float64, button string, delay time.Duration) error {
	if err := p.mouseDown(ctx,
		x, y, button, 1); err != nil {
		return err
	}
	if !p.fastActionMode() {
		if err := waitInputDelay(ctx, delay); err != nil {
			return err
		}
	}
	return p.mouseUp(ctx,
		x, y, button, 1)
}

func (p *Page) dispatchMouseDoubleClick(ctx context.Context,

	x, y float64) error {
	if err := p.mouseDown(ctx,
		x, y, "left", 1); err != nil {
		return err
	}
	if err := p.mouseUp(ctx,
		x, y, "left", 1); err != nil {
		return err
	}
	if !p.fastActionMode() {
		if err := waitInputDelay(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
	if err := p.mouseDown(ctx,
		x, y, "left", 2); err != nil {
		return err
	}
	return p.mouseUp(ctx,
		x, y, "left", 2)
}

// MouseClick 执行一次完整的点击序列 (Move -> Down -> Up)
func (p *Page) MouseClick(x, y float64) error {
	return p.MouseClickContext(p.ctx, x, y)
}

func (p *Page) MouseClickContext(ctx context.Context, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.mouseClick(ctx, x, y, "left")
	})
}

func (p *Page) mouseClick(ctx context.Context, x, y float64, button string) error {
	kind := "click"
	if button == "right" {
		kind = "right-click"
	}
	p.showMouseAction(ctx, mouseVisualAction{Kind: kind, X: x, Y: y})
	p.showActionTarget(ctx, p.pointTargetPreviewBoxes(ctx, x, y))
	if err := p.mouseMove(ctx, x, y); err != nil {
		return err
	}
	if !p.fastActionMode() {
		if err := waitInputDelay(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	return p.dispatchMouseClick(ctx, x, y, button, 50*time.Millisecond)
}

// MouseDoubleClick 执行双击序列
func (p *Page) MouseDoubleClick(x, y float64) error {
	return p.MouseDoubleClickContext(p.ctx, x, y)
}

func (p *Page) MouseDoubleClickContext(ctx context.Context, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		p.showMouseAction(ctx, mouseVisualAction{Kind: "double-click", X: x, Y: y})
		p.showActionTarget(ctx, p.pointTargetPreviewBoxes(ctx, x, y))
		if err := p.mouseMove(ctx, x, y); err != nil {
			return err
		}
		return p.dispatchMouseDoubleClick(ctx, x, y)
	})
}

// MouseDrag 执行拖拽操作
func (p *Page) MouseDrag(fromX, fromY, toX, toY float64) error {
	return p.MouseDragContext(p.ctx, fromX, fromY, toX, toY)
}

func (p *Page) MouseDragContext(ctx context.Context, fromX, fromY, toX, toY float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.mouseDrag(ctx,
			fromX, fromY, toX, toY)
	})
}

func (p *Page) mouseDrag(ctx context.Context,

	fromX, fromY, toX, toY float64) error {
	p.showMouseAction(ctx,
		mouseVisualAction{Kind: "drag", FromX: fromX, FromY: fromY, ToX: toX, ToY: toY})
	if err := p.mouseMove(ctx,
		fromX, fromY); err != nil {
		return err
	}
	if !p.fastActionMode() {
		if err := waitInputDelay(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}

	if err := p.mouseDown(ctx,
		fromX, fromY, "left", 1); err != nil {
		return err
	}
	if !p.fastActionMode() {
		if err := waitInputDelay(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}

	const dragSteps = 10
	for step := 1; step <= dragSteps; step++ {
		ratio := float64(step) / dragSteps
		x := fromX + (toX-fromX)*ratio
		y := fromY + (toY-fromY)*ratio
		if err := BrowserErrorFromCDP("Input.dispatchMouseEvent", topPageExecutionTarget(), p.CdpConn.SendPacketContext(ctx,
			"Input.dispatchMouseEvent", map[string]any{
				"type": "mouseMoved", "x": x, "y": y, "button": "left", "buttons": 1,
			})); err != nil {
			return err
		}
		p.rememberMousePosition(x, y)
	}
	if !p.fastActionMode() {
		if err := waitInputDelay(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}

	return p.mouseUp(ctx,
		toX, toY, "left", 1)
}

func (p *Page) MouseWheel(x, y, deltaX, deltaY float64) error {
	return p.MouseWheelContext(p.ctx, x, y, deltaX, deltaY)
}

func (p *Page) MouseWheelContext(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.mouseWheel(ctx,
			x, y, deltaX, deltaY)
	})
}

func (p *Page) mouseWheel(ctx context.Context,

	x, y, deltaX, deltaY float64) error {
	p.showMouseAction(ctx,
		mouseVisualAction{Kind: "wheel", X: x, Y: y})
	err := BrowserErrorFromCDP("Input.dispatchMouseEvent", topPageExecutionTarget(), p.CdpConn.SendPacketContext(ctx,
		"Input.dispatchMouseEvent", mouseWheelEventParams(x, y, deltaX, deltaY)))
	if err == nil {
		p.rememberMousePosition(x, y)
	}
	return err
}

func mouseWheelEventParams(x, y, deltaX, deltaY float64) map[string]any {
	return map[string]any{
		"type": "mouseWheel", "x": x, "y": y, "deltaX": deltaX, "deltaY": deltaY,
		"button": "none", "buttons": 0,
	}
}

// MouseRightClick 执行右键点击
func (p *Page) MouseRightClick(x, y float64) error {
	return p.MouseRightClickContext(p.ctx, x, y)
}

func (p *Page) MouseRightClickContext(ctx context.Context, x, y float64) error {
	return p.runForegroundInteractionContext(ctx, func() error {
		return p.mouseClick(ctx, x, y, "right")
	})
}

func (p *Page) inputDispatchMouseEvent(ctx context.Context,

	typ string, x, y float64, button string, clickCount int) error {
	err := p.CdpConn.SendPacketContext(ctx,
		"Input.dispatchMouseEvent", map[string]any{
			"type":       typ,
			"x":          x,
			"y":          y,
			"button":     button,
			"clickCount": clickCount,
		})
	return BrowserErrorFromCDP("Input.dispatchMouseEvent", topPageExecutionTarget(), err)
}

func waitInputDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
