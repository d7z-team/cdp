import {DESIGN_TOKENS} from "../design";
import {withAlpha} from "./color";
import {applyNonInteractive, ensureOverlayHost, removeElement} from "./dom";
import {Box, DrawOptions, DrawOwner, HighlightConfig} from "./types";
import {internalElementID} from "../utils/internal_ids";

interface HighlightState {
    boxes: Box[];
    timeoutMs: number;
    clearTimer: ReturnType<typeof setTimeout> | null;
    color: string;
    revision: number;
}

const DEFAULT_DRAW_OWNER: DrawOwner = 'default';
const DRAW_OWNER_ORDER: DrawOwner[] = [
    'page-highlight',
    'default',
    'action-preview',
];

export class CanvasRenderer {
    private static readonly DEFAULT_HIGHLIGHT_TIMEOUT_MS = 5000;

    private readonly canvasHostID: string;
    private readonly canvasID: string;
    private readonly boundResize: () => void;
    private readonly boundScroll: () => void;
    private canvas: HTMLCanvasElement | null = null;
    private ctx: CanvasRenderingContext2D | null = null;
    private canvasHost: HTMLDivElement | null = null;
    private viewportListenersBound = false;
    private currentModeColor = '#ff0000';
    private readonly layers = new Map<DrawOwner, HighlightState>();

    public constructor(
        id: string,
        private readonly highlightConfig: HighlightConfig,
    ) {
        this.canvasHostID = internalElementID(`overlay_canvas_host_${id}`);
        this.canvasID = internalElementID(`box_drawer_canvas_${id}`);
        this.boundResize = this.resizeCanvas.bind(this);
        this.boundScroll = this.handleViewportScroll.bind(this);
    }

    public setHighlightColor(color: string, options: DrawOptions = {}): void {
        const layer = this.prepareLayer(options);
        if (!layer) return;
        layer.color = color;
        if (!options.owner) {
            this.currentModeColor = color;
        }
        if (this.canvas && this.ctx) {
            this.renderCanvas();
        }
    }

    public draw(x: number, y: number, width: number, height: number, radius = 0, options: DrawOptions = {}): void {
        this.drawMultiple([{x, y, width, height, radius}], options);
    }

    public drawMultiple(boxes: Box[], options: DrawOptions = {}): void {
        const layer = this.prepareLayer(options);
        if (!layer) return;
        layer.boxes = boxes.filter(box => this.validBox(box));
        this.resetLayerTimer(layer);
        this.scheduleLayerClear(this.ownerFromOptions(options), layer, options.timeoutMs);
        this.renderCanvas();
    }

    public setHighlightTimeout(ms: number, options: DrawOptions = {}): void {
        if (!Number.isFinite(ms)) {
            return;
        }
        const layer = this.prepareLayer(options);
        if (!layer) return;
        layer.timeoutMs = Math.max(0, Math.floor(ms));
    }

    public clear(options: DrawOptions = {}): void {
        if (options.owner) {
            this.clearLayer(options.owner, options.revision);
            if (this.canvas && this.ctx) this.renderCanvas();
            return;
        }
        for (const layer of this.layers.values()) {
            this.resetLayerTimer(layer);
            layer.boxes = [];
            layer.revision += 1;
        }
        if (this.canvas && this.ctx) this.renderCanvas();
    }

    public destroy(): void {
        this.clear();
        removeElement(this.canvas);
        this.canvas = null;
        this.ctx = null;
        removeElement(this.canvasHost);
        this.canvasHost = null;
        this.unbindViewportListeners();
    }

    private resetLayerTimer(layer: HighlightState): void {
        if (layer.clearTimer) {
            clearTimeout(layer.clearTimer);
            layer.clearTimer = null;
        }
    }

    private ownerFromOptions(options: DrawOptions = {}): DrawOwner {
        return options.owner || DEFAULT_DRAW_OWNER;
    }

    private layerFor(owner: DrawOwner): HighlightState {
        let layer = this.layers.get(owner);
        if (!layer) {
            layer = {
                boxes: [],
                timeoutMs: CanvasRenderer.DEFAULT_HIGHLIGHT_TIMEOUT_MS,
                clearTimer: null,
                color: this.currentModeColor,
                revision: 0,
            };
            this.layers.set(owner, layer);
        }
        return layer;
    }

    private prepareLayer(options: DrawOptions = {}): HighlightState | null {
        const owner = this.ownerFromOptions(options);
        const layer = this.layerFor(owner);
        const nextRevision = Number.isFinite(options.revision) ? Math.floor(options.revision || 0) : layer.revision + 1;
        if (nextRevision < layer.revision) {
            return null;
        }
        layer.revision = nextRevision;
        if (options.color) {
            layer.color = options.color;
        }
        if (Number.isFinite(options.timeoutMs)) {
            layer.timeoutMs = Math.max(0, Math.floor(options.timeoutMs || 0));
        }
        return layer;
    }

    private clearLayer(owner: DrawOwner, revision?: number): void {
        const layer = this.layerFor(owner);
        const nextRevision = Number.isFinite(revision) ? Math.floor(revision || 0) : layer.revision + 1;
        if (nextRevision < layer.revision) {
            return;
        }
        layer.revision = nextRevision;
        this.resetLayerTimer(layer);
        layer.boxes = [];
    }

    private scheduleLayerClear(owner: DrawOwner, layer: HighlightState, timeoutMs?: number): void {
        const ms = Number.isFinite(timeoutMs) ? Math.max(0, Math.floor(timeoutMs || 0)) : layer.timeoutMs;
        if (ms <= 0) return;
        const revision = layer.revision;
        layer.clearTimer = setTimeout(() => {
            this.clearLayer(owner, revision);
            this.renderCanvas();
        }, ms);
    }

    private renderCanvas(): void {
        if (!this.canvas || !this.canvas.isConnected) {
            this.initCanvas();
        }

        if (!this.ctx) {
            return;
        }

        this.ctx.save();
        this.clearCanvasSurface();
        for (const owner of DRAW_OWNER_ORDER) {
            const layer = this.layers.get(owner);
            if (!layer) continue;
            for (const [index, box] of layer.boxes.entries()) {
                this.drawRect(box.x, box.y, box.width, box.height, box.radius ?? 0, index === 0, layer.color);
            }
        }
        this.ctx.restore();
    }

    private initCanvas(): void {
        const root = this.ensureCanvasRoot();
        if (this.canvas?.parentNode) {
            this.canvas.parentNode.removeChild(this.canvas);
        }

        this.canvas = document.createElement('canvas');
        this.canvas.id = this.canvasID;
        Object.assign(this.canvas.style, {
            position: 'fixed',
            top: '0px',
            left: '0px',
            zIndex: String(DESIGN_TOKENS.zCanvas),
        });
        applyNonInteractive(this.canvas);
        root.appendChild(this.canvas);
        this.ctx = this.canvas.getContext('2d');
        this.resizeCanvas();
        this.bindViewportListeners();
    }

    private resizeCanvas(shouldRefresh = true): void {
        if (!this.canvas) {
            return;
        }

        const viewport = window.visualViewport;
        const dpr = window.devicePixelRatio || 1;
        const width = viewport?.width || window.innerWidth;
        const height = viewport?.height || window.innerHeight;
        const offsetLeft = viewport?.offsetLeft || 0;
        const offsetTop = viewport?.offsetTop || 0;

        this.canvas.width = width * dpr;
        this.canvas.height = height * dpr;
        this.canvas.style.width = `${width}px`;
        this.canvas.style.height = `${height}px`;
        this.canvas.style.left = `${offsetLeft}px`;
        this.canvas.style.top = `${offsetTop}px`;
        applyNonInteractive(this.canvas);

        if (this.ctx) {
            this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        }
        if (shouldRefresh && this.ctx) {
            this.renderCanvas();
        }
    }

    private handleViewportScroll(): void {
        if (!this.canvas) {
            return;
        }

        const hadHighlight = Array.from(this.layers.values()).some(layer => layer.boxes.length > 0);
        if (hadHighlight) {
            for (const layer of this.layers.values()) {
                this.resetLayerTimer(layer);
                layer.boxes = [];
                layer.revision += 1;
            }
        }

        this.resizeCanvas(!hadHighlight);
        if (hadHighlight) {
            this.clearCanvasSurface();
        }
    }

    private clearCanvasSurface(): void {
        if (!this.canvas || !this.ctx) {
            return;
        }
        this.ctx.save();
        this.ctx.setTransform(1, 0, 0, 1, 0, 0);
        this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
        this.ctx.restore();
    }

    private validBox(box: Box): boolean {
        return Number.isFinite(box?.x) && Number.isFinite(box?.y) && Number.isFinite(box?.width) && Number.isFinite(box?.height) && box.width > 0 && box.height > 0;
    }

    private drawRect(x: number, y: number, width: number, height: number, radius: number, primary: boolean, color: string): void {
        if (!this.ctx) {
            return;
        }

        const strokeWidth = primary ? this.highlightConfig.borderWidth : Math.max(1, this.highlightConfig.borderWidth - 0.5);
        const fillOpacity = primary ? this.highlightConfig.opacity : this.highlightConfig.opacity * 0.45;
        const actualRadius = radius > 0 ? radius : Math.min(8, width / 5, height / 5);
        const glowColor = withAlpha(color, primary ? 0.30 : 0.18);
        const edgeColor = withAlpha(color, primary ? 0.98 : 0.62);
        const innerEdgeColor = withAlpha('#ffffff', primary ? 0.88 : 0.50);
        const fillTop = withAlpha(color, fillOpacity * 0.95);
        const fillBottom = withAlpha(color, fillOpacity * 0.25);
        const cornerLength = Math.max(8, Math.min(18, Math.min(width, height) * 0.18));

        this.ctx.save();
        this.ctx.shadowColor = glowColor;
        this.ctx.shadowBlur = primary ? 18 : 10;
        this.ctx.shadowOffsetX = 0;
        this.ctx.shadowOffsetY = 0;
        this.ctx.strokeStyle = glowColor;
        this.ctx.lineWidth = strokeWidth + (primary ? 3 : 2);
        this.drawRoundedRect(x, y, width, height, actualRadius);
        this.ctx.stroke();

        this.ctx.shadowColor = 'transparent';
        this.ctx.shadowBlur = 0;
        this.ctx.strokeStyle = edgeColor;
        this.ctx.lineWidth = strokeWidth;
        this.drawRoundedRect(x, y, width, height, actualRadius);
        this.ctx.stroke();

        this.ctx.strokeStyle = innerEdgeColor;
        this.ctx.lineWidth = 1;
        this.drawRoundedRect(
            x + 0.5,
            y + 0.5,
            Math.max(0, width - 1),
            Math.max(0, height - 1),
            Math.max(0, actualRadius - 0.5),
        );
        this.ctx.stroke();

        const gradient = this.ctx.createLinearGradient(x, y, x, y + height);
        gradient.addColorStop(0, fillTop);
        gradient.addColorStop(1, fillBottom);
        this.ctx.fillStyle = gradient;
        this.drawRoundedRect(x, y, width, height, actualRadius);
        this.ctx.fill();

        this.drawCornerAccents(x, y, width, height, actualRadius, cornerLength, edgeColor, strokeWidth + 0.5);
        this.ctx.restore();
    }

    private drawRoundedRect(x: number, y: number, width: number, height: number, radius: number): void {
        if (!this.ctx) {
            return;
        }
        this.ctx.beginPath();
        this.ctx.moveTo(x + radius, y);
        this.ctx.lineTo(x + width - radius, y);
        this.ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
        this.ctx.lineTo(x + width, y + height - radius);
        this.ctx.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
        this.ctx.lineTo(x + radius, y + height);
        this.ctx.quadraticCurveTo(x, y + height, x, y + height - radius);
        this.ctx.lineTo(x, y + radius);
        this.ctx.quadraticCurveTo(x, y, x + radius, y);
        this.ctx.closePath();
    }

    private drawCornerAccents(
        x: number,
        y: number,
        width: number,
        height: number,
        radius: number,
        length: number,
        color: string,
        lineWidth: number,
    ): void {
        if (!this.ctx) {
            return;
        }

        const horizontal = Math.max(0, Math.min(length, width * 0.4));
        const vertical = Math.max(0, Math.min(length, height * 0.4));
        this.ctx.save();
        this.ctx.strokeStyle = color;
        this.ctx.lineWidth = lineWidth;
        this.ctx.lineCap = 'round';
        this.ctx.lineJoin = 'round';
        this.ctx.beginPath();
        this.ctx.moveTo(x + radius, y);
        this.ctx.lineTo(x + radius + horizontal, y);
        this.ctx.moveTo(x, y + radius);
        this.ctx.lineTo(x, y + radius + vertical);
        this.ctx.moveTo(x + width - radius, y);
        this.ctx.lineTo(x + width - radius - horizontal, y);
        this.ctx.moveTo(x + width, y + radius);
        this.ctx.lineTo(x + width, y + radius + vertical);
        this.ctx.moveTo(x + radius, y + height);
        this.ctx.lineTo(x + radius + horizontal, y + height);
        this.ctx.moveTo(x, y + height - radius);
        this.ctx.lineTo(x, y + height - radius - vertical);
        this.ctx.moveTo(x + width - radius, y + height);
        this.ctx.lineTo(x + width - radius - horizontal, y + height);
        this.ctx.moveTo(x + width, y + height - radius);
        this.ctx.lineTo(x + width, y + height - radius - vertical);
        this.ctx.stroke();
        this.ctx.restore();
    }

    private ensureCanvasRoot(): ShadowRoot {
        const {host, root} = ensureOverlayHost(this.canvasHost, this.canvasHostID, DESIGN_TOKENS.zCanvas);
        this.canvasHost = host;
        return root;
    }

    private bindViewportListeners(): void {
        if (this.viewportListenersBound) {
            return;
        }
        window.addEventListener('resize', this.boundResize, {passive: true});
        window.addEventListener('scroll', this.boundScroll, {passive: true});
        window.visualViewport?.addEventListener('resize', this.boundResize, {passive: true});
        window.visualViewport?.addEventListener('scroll', this.boundScroll, {passive: true});
        this.viewportListenersBound = true;
    }

    private unbindViewportListeners(): void {
        if (!this.viewportListenersBound) {
            return;
        }
        window.removeEventListener('resize', this.boundResize);
        window.removeEventListener('scroll', this.boundScroll);
        window.visualViewport?.removeEventListener('resize', this.boundResize);
        window.visualViewport?.removeEventListener('scroll', this.boundScroll);
        this.viewportListenersBound = false;
    }
}
