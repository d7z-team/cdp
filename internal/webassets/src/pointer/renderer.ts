import {DESIGN_TOKENS} from "../design";
import {applyNonInteractive, ensureOverlayHost, removeElement} from "../canvas/dom";
import {internalElementID} from "../utils/internal_ids";

export const POINTER_HOST_ID = internalElementID('pointer_overlay_host');
export const POINTER_CANVAS_ID = internalElementID('pointer_canvas');

export class PointerRenderer {
    private static readonly CURSOR_SCALE = 0.66;

    private host: HTMLDivElement | null = null;
    private canvas: HTMLCanvasElement | null = null;
    private ctx: CanvasRenderingContext2D | null = null;
    private point: {x: number; y: number; pressed: boolean} | null = null;
    private readonly resize = () => this.resizeCanvas();

    public show(x: number, y: number, pressed: boolean): void {
        if (!Number.isFinite(x) || !Number.isFinite(y)) return;
        this.point = {x, y, pressed};
        this.ensureCanvas();
        if (!this.canvas) return;
        this.canvas.style.display = 'block';
        this.render();
    }

    public hide(): void {
        this.point = null;
        this.clearSurface();
        if (this.canvas) this.canvas.style.display = 'none';
    }

    public destroy(): void {
        window.removeEventListener('resize', this.resize);
        window.visualViewport?.removeEventListener('resize', this.resize);
        removeElement(this.canvas);
        this.canvas = null;
        this.ctx = null;
        removeElement(this.host);
        this.host = null;
        this.point = null;
    }

    private ensureCanvas(): void {
        if (this.canvas?.isConnected && this.ctx) return;
        const {host, root} = ensureOverlayHost(this.host, POINTER_HOST_ID, DESIGN_TOKENS.zCanvas + 1);
        this.host = host;
        this.canvas = document.createElement('canvas');
        this.canvas.id = POINTER_CANVAS_ID;
        Object.assign(this.canvas.style, {
            position: 'fixed',
            top: '0px',
            left: '0px',
            zIndex: String(DESIGN_TOKENS.zCanvas + 1),
            display: 'none',
        });
        applyNonInteractive(this.canvas);
        root.appendChild(this.canvas);
        this.ctx = this.canvas.getContext('2d');
        window.addEventListener('resize', this.resize, {passive: true});
        window.visualViewport?.addEventListener('resize', this.resize, {passive: true});
        this.resizeCanvas();
    }

    private resizeCanvas(): void {
        if (!this.canvas) return;
        const viewport = window.visualViewport;
        const dpr = window.devicePixelRatio || 1;
        const width = viewport?.width || window.innerWidth;
        const height = viewport?.height || window.innerHeight;
        this.canvas.width = width * dpr;
        this.canvas.height = height * dpr;
        this.canvas.style.width = `${width}px`;
        this.canvas.style.height = `${height}px`;
        this.canvas.style.left = `${viewport?.offsetLeft || 0}px`;
        this.canvas.style.top = `${viewport?.offsetTop || 0}px`;
        applyNonInteractive(this.canvas);
        this.ctx?.setTransform(dpr, 0, 0, dpr, 0, 0);
        this.render();
    }

    private render(): void {
        this.clearSurface();
        if (!this.ctx || !this.point) return;
        const {x, y, pressed} = this.point;
        const ctx = this.ctx;
        const s = PointerRenderer.CURSOR_SCALE;

        ctx.save();
        ctx.translate(x, y);

        let shape: Path2D;
        let innerShape: Path2D | null = null;
        let strokeWidth = 1.05;
        let shadowBlur = 5;
        let shadowOffsetX = 2;
        let shadowOffsetY = 2.6;

        if (pressed) {
            shape = new Path2D(`
                M 11.2 ${0.5 * s}
                C 9.8 ${0.5 * s}, 8.9 ${1.6 * s}, 8.9 ${3.1 * s}
                L 8.9 ${13.6 * s}
                L 6.8 ${11.7 * s}
                C 5.6 ${10.7 * s}, 3.7 ${10.9 * s}, 2.8 ${12.3 * s}
                C 2.0 ${13.4 * s}, 2.2 ${15.0 * s}, 3.1 ${16.0 * s}
                L 8.3 ${21.9 * s}
                C 9.4 ${23.2 * s}, 11.0 ${24.0 * s}, 12.7 ${24.0 * s}
                L 18.9 ${24.0 * s}
                C 21.7 ${24.0 * s}, 24.0 ${21.8 * s}, 24.0 ${19.0 * s}
                L 24.0 ${9.1 * s}
                C 24.0 ${7.8 * s}, 23.1 ${6.7 * s}, 21.9 ${6.4 * s}
                C 21.0 ${6.1 * s}, 20.0 ${6.4 * s}, 19.2 ${7.1 * s}
                L 19.2 ${4.9 * s}
                C 19.2 ${3.6 * s}, 18.4 ${2.5 * s}, 17.1 ${2.2 * s}
                C 16.2 ${1.9 * s}, 15.1 ${2.2 * s}, 14.4 ${2.9 * s}
                L 14.4 ${4.5 * s}
                L 13.6 ${4.5 * s}
                L 13.6 ${3.1 * s}
                C 13.6 ${1.6 * s}, 12.6 ${0.5 * s}, 11.2 ${0.5 * s}
                Z
            `);
            innerShape = new Path2D(`
                M 11.3 ${2.5 * s}
                L 11.3 ${14.3 * s}
                M 14.5 ${5.2 * s}
                L 14.5 ${13.8 * s}
                M 17.6 ${5.0 * s}
                L 17.6 ${13.6 * s}
                M 20.7 ${8.5 * s}
                L 20.7 ${14.4 * s}
                M 6.3 ${13.6 * s}
                L 11.0 ${18.9 * s}
            `);
            strokeWidth = 0.95;
            shadowBlur = 5.8;
            shadowOffsetX = 2.2;
            shadowOffsetY = 2.9;
        } else {
            shape = new Path2D(`
                M 0 0
                L 0 ${30.8 * s}
                L ${7.4 * s} ${23.5 * s}
                L ${11.8 * s} ${35.0 * s}
                L ${17.4 * s} ${32.9 * s}
                L ${13.0 * s} ${21.8 * s}
                L ${25.2 * s} ${21.8 * s}
                Z
            `);
            innerShape = new Path2D(`
                M ${2.7 * s} ${6.3 * s}
                L ${2.7 * s} ${23.7 * s}
                L ${8.1 * s} ${18.4 * s}
                L ${12.8 * s} ${29.8 * s}
            `);
        }

        ctx.save();
        ctx.shadowColor = 'rgba(0, 0, 0, 0.34)';
        ctx.shadowBlur = shadowBlur;
        ctx.shadowOffsetX = shadowOffsetX;
        ctx.shadowOffsetY = shadowOffsetY;
        ctx.fillStyle = 'rgba(0, 0, 0, 0.20)';
        ctx.fill(shape);
        ctx.restore();

        ctx.fillStyle = '#ffffff';
        ctx.fill(shape);
        ctx.strokeStyle = '#111111';
        ctx.lineWidth = strokeWidth;
        ctx.lineJoin = 'round';
        ctx.lineCap = 'round';
        ctx.stroke(shape);

        if (innerShape) {
            ctx.strokeStyle = pressed ? 'rgba(205, 214, 224, 0.82)' : 'rgba(255, 255, 255, 0.84)';
            ctx.lineWidth = pressed ? 0.72 : 0.62;
            ctx.stroke(innerShape);
        }
        ctx.restore();
    }

    private clearSurface(): void {
        if (!this.canvas || !this.ctx) return;
        this.ctx.save();
        this.ctx.setTransform(1, 0, 0, 1, 0, 0);
        this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
        this.ctx.restore();
    }
}
