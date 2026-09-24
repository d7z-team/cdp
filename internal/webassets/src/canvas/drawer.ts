import {CanvasRenderer} from "./renderer";
import {
    Box, DrawOptions, ICdpCanvas,
    DEFAULT_HIGHLIGHT_CONFIG, HighlightConfig,
} from "./types";
import {FrameBridge, Channel} from "../bridge";

export type {Box, DrawOptions, ICdpCanvas, HighlightConfig};

export class IframeBoxDraw implements ICdpCanvas {
    public constructor(private readonly bridge: FrameBridge) {}

    private postCmd(cmd: string, payload: Record<string, unknown> = {}): void {
        this.bridge.post(Channel.CANVAS_CMD, {cmd, ...payload}, {target: 'parent'});
    }

    public setMode(color: string, modeName?: string, options?: DrawOptions): void {
        this.postCmd('setMode', {color, modeName, options});
    }

    public setHighlightColor(color: string, options?: DrawOptions): void {
        this.postCmd('setHighlightColor', {color, options});
    }

    public draw(x: number, y: number, width: number, height: number, radius: number, options?: DrawOptions): void {
        this.drawMultiple([{x, y, width, height, radius}], options);
    }
    public drawMultiple(boxes: Box[], options?: DrawOptions): void { this.postCmd('drawMultiple', {boxes, options}); }
    public setHighlightTimeout(ms: number, options?: DrawOptions): void { this.postCmd('setHighlightTimeout', {timeout: ms, options}); }
    public clear(options?: DrawOptions): void { this.postCmd('clear', {options}); }
    public destroy(): void {
        // Rendering resources are owned by the top frame; this proxy owns none.
    }
}

export class BoxDrawer implements ICdpCanvas {
    private readonly renderer: CanvasRenderer;

    public constructor(id: string, _bridge: FrameBridge, customConfig: Partial<HighlightConfig> = {}) {
        const highlightConfig: HighlightConfig = {
            ...DEFAULT_HIGHLIGHT_CONFIG,
            ...customConfig,
        };
        this.renderer = new CanvasRenderer(id, highlightConfig);
    }

    public setHighlightColor(color: string, options?: DrawOptions): void {
        this.renderer.setHighlightColor(color, options);
    }

    public setMode(color: string, modeName?: string, options?: DrawOptions): void {
        this.renderer.setHighlightColor(color, {...options, modeName});
    }

    public draw(x: number, y: number, width: number, height: number, radius: number, options?: DrawOptions): void {
        this.renderer.draw(x, y, width, height, radius, options);
    }

    public drawMultiple(boxes: Box[], options?: DrawOptions): void {
        this.renderer.drawMultiple(boxes, options);
    }

    public setHighlightTimeout(ms: number, options?: DrawOptions): void {
        this.renderer.setHighlightTimeout(ms, options);
    }

    public clear(options?: DrawOptions): void {
        this.renderer.clear(options);
    }

    public destroy(): void {
        this.renderer.destroy();
    }
}
