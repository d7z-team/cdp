import {Box, DrawOptions, ICdpCanvas, BoxDrawer, IframeBoxDraw} from "./drawer";
import {FrameBridge, Channel} from "../bridge";

const DRAW_ID = "core";

type CanvasCmd = {
    cmd: string;
    color?: string;
    modeName?: string;
    x?: number;
    y?: number;
    width?: number;
    height?: number;
    radius?: number;
    timeout?: number;
    boxes?: Box[];
    options?: DrawOptions;
};

class ManagedCanvas implements ICdpCanvas {
    private readonly canvas: ICdpCanvas;
    private readonly unsubs: (() => void)[] = [];
    private destroyed = false;

    constructor(bridge: FrameBridge) {
        const insideFrame = window.parent !== window;

        if (insideFrame) {
            this.canvas = new IframeBoxDraw(bridge);
        } else {
            this.canvas = new BoxDrawer(DRAW_ID, bridge);
        }
        this.unsubs.push(bridge.onMessage(Channel.CANVAS_CMD, (data: CanvasCmd) => {
            this.applyCmd(data);
        }));
    }

    private applyCmd(data: CanvasCmd): void {
        switch (data.cmd) {
            case 'setMode': this.canvas.setMode(data.color ?? '#ff0000', data.modeName, data.options); break;
            case 'setHighlightColor': this.canvas.setHighlightColor(data.color ?? '#ff0000', data.options); break;
            case 'draw': this.canvas.draw(data.x ?? 0, data.y ?? 0, data.width ?? 0, data.height ?? 0, data.radius ?? 0, data.options); break;
            case 'drawMultiple':
                if (Array.isArray(data.boxes)) this.canvas.drawMultiple(data.boxes, data.options);
                break;
            case 'setHighlightTimeout': this.canvas.setHighlightTimeout(data.timeout ?? 0, data.options); break;
            case 'clear': this.canvas.clear(data.options); break;
            case 'destroy': this.canvas.destroy(); break;
        }
    }

    public setHighlightColor(color: string, options?: DrawOptions): void { this.canvas.setHighlightColor(color, options); }
    public setMode(color: string, modeName?: string, options?: DrawOptions): void { this.canvas.setMode(color, modeName, options); }
    public draw(x: number, y: number, width: number, height: number, radius: number, options?: DrawOptions): void { this.canvas.draw(x, y, width, height, radius, options); }
    public drawMultiple(boxes: Box[], options?: DrawOptions): void {
        this.canvas.drawMultiple(boxes, options);
    }
    public setHighlightTimeout(ms: number, options?: DrawOptions): void { this.canvas.setHighlightTimeout(ms, options); }
    public clear(options?: DrawOptions): void { this.canvas.clear(options); }

    public destroy(): void {
        if (this.destroyed) return;
        this.destroyed = true;
        for (const unsub of this.unsubs) unsub();
        this.unsubs.length = 0;
        this.canvas.destroy();
    }

}

export function createCanvas(bridge: FrameBridge): ICdpCanvas {
    return new ManagedCanvas(bridge);
}
