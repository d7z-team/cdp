import {PointerRenderer} from "./renderer";
import {MouseVisualAction} from "./types";

interface Point {
    x: number;
    y: number;
}

interface VisualSegment {
    from: Point;
    to: Point;
    durationMs: number;
    pressed: boolean;
}

const FRAME_DELAY_MS = 16;
const VISIBLE_AFTER_ACTION_MS = 1200;

export class MouseVisualController {
    private readonly renderer = new PointerRenderer();
    private point: Point | null = null;
    private generation = 0;
    private timer: ReturnType<typeof setTimeout> | null = null;
    private destroyed = false;

    public perform(action: MouseVisualAction): void {
        if (this.destroyed || !this.validAction(action)) return;
        const generation = ++this.generation;
        this.cancelTimer();

        const target = action.kind === 'drag'
            ? {x: action.toX, y: action.toY}
            : {x: action.x, y: action.y};
        const start = this.point || (action.kind === 'drag' ? {x: action.fromX, y: action.fromY} : target);
        const moveDuration = action.mode === 'fast' ? 0 : this.moveDuration(start, target);
        const pressDuration = action.mode === 'fast' ? 48 : 80;
        const segments: VisualSegment[] = [];

        if (action.kind === 'drag') {
            const from = {x: action.fromX, y: action.fromY};
            segments.push({from: start, to: from, durationMs: action.mode === 'fast' ? 0 : this.moveDuration(start, from), pressed: false});
            segments.push({from, to: from, durationMs: pressDuration, pressed: true});
            segments.push({from, to: target, durationMs: action.mode === 'fast' ? 0 : this.moveDuration(from, target), pressed: true});
        } else {
            segments.push({from: start, to: target, durationMs: moveDuration, pressed: false});
            if (action.kind === 'click' || action.kind === 'right-click') {
                segments.push({from: target, to: target, durationMs: pressDuration, pressed: true});
            } else if (action.kind === 'double-click') {
                segments.push({from: target, to: target, durationMs: pressDuration, pressed: true});
                segments.push({from: target, to: target, durationMs: pressDuration, pressed: false});
                segments.push({from: target, to: target, durationMs: pressDuration, pressed: true});
            } else if (action.kind === 'press') {
                segments.push({from: target, to: target, durationMs: 700, pressed: true});
            }
        }

        this.runTimeline(generation, segments, target);
    }

    public destroy(): void {
        if (this.destroyed) return;
        this.destroyed = true;
        this.generation++;
        this.cancelTimer();
        this.renderer.destroy();
        this.point = null;
    }

    private runTimeline(generation: number, segments: VisualSegment[], target: Point): void {
        const startedAt = performance.now();
        const totalDuration = segments.reduce((sum, segment) => sum + segment.durationMs, 0);
        const tick = () => {
            if (this.destroyed || generation !== this.generation) return;
            const elapsed = performance.now() - startedAt;
            let segmentStart = 0;

            for (const segment of segments) {
                const segmentEnd = segmentStart + segment.durationMs;
                if (elapsed <= segmentEnd || segment === segments[segments.length - 1]) {
                    const progress = segment.durationMs <= 0 ? 1 : Math.max(0, Math.min(1, (elapsed - segmentStart) / segment.durationMs));
                    const x = segment.from.x + (segment.to.x - segment.from.x) * progress;
                    const y = segment.from.y + (segment.to.y - segment.from.y) * progress;
                    this.renderer.show(x, y, segment.pressed);
                    break;
                }
                segmentStart = segmentEnd;
            }

            if (elapsed < totalDuration) {
                this.timer = setTimeout(tick, FRAME_DELAY_MS);
                return;
            }

            this.point = target;
            this.renderer.show(target.x, target.y, false);
            this.timer = setTimeout(() => {
                if (generation !== this.generation || this.destroyed) return;
                this.timer = null;
                this.renderer.hide();
            }, VISIBLE_AFTER_ACTION_MS);
        };
        tick();
    }

    private moveDuration(from: Point, to: Point): number {
        const distance = Math.hypot(to.x - from.x, to.y - from.y);
        if (distance < 1) return 0;
        return Math.max(70, Math.min(180, 55 + distance * 0.12));
    }

    private validAction(action: MouseVisualAction): boolean {
        if (!action || (action.mode !== 'strict' && action.mode !== 'fast')) return false;
        if (action.kind === 'drag') {
            return [action.fromX, action.fromY, action.toX, action.toY].every(Number.isFinite);
        }
        return ['move', 'click', 'double-click', 'right-click', 'wheel', 'press', 'release'].includes(action.kind)
            && Number.isFinite(action.x)
            && Number.isFinite(action.y);
    }

    private cancelTimer(): void {
        if (!this.timer) return;
        clearTimeout(this.timer);
        this.timer = null;
    }
}
