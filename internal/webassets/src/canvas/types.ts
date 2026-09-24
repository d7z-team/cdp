export interface Box {
    x: number;
    y: number;
    width: number;
    height: number;
    radius?: number;
}

export type DrawOwner =
    | 'default'
    | 'page-highlight'
    | 'action-preview';

export interface DrawOptions {
    owner?: DrawOwner;
    revision?: number;
    timeoutMs?: number;
    color?: string;
    modeName?: string;
}

export interface HighlightConfig {
    borderColor: string;
    borderWidth: number;
    opacity: number;
    labelFontSize: number;
    labelFont: string;
}

export const DEFAULT_HIGHLIGHT_CONFIG: HighlightConfig = {
    borderColor: '#ff0000',
    borderWidth: 2,
    opacity: 0.1,
    labelFontSize: 12,
    labelFont: 'Arial, sans-serif',
};

export const DEFAULT_HIGHLIGHT_TIMEOUT_MS = 5000;

export const DEFAULT_HIGHLIGHT_COLOR = '#ff0000';

export interface DrawService {
    setMode(color: string, modeName?: string, options?: DrawOptions): void;
    setHighlightColor(color: string, options?: DrawOptions): void;
    draw(x: number, y: number, width: number, height: number, radius?: number, options?: DrawOptions): void;
    drawMultiple(boxes: Box[], options?: DrawOptions): void;
    setHighlightTimeout(ms: number, options?: DrawOptions): void;
    clear(options?: DrawOptions): void;
    destroy(): void;
}

export type ICdpCanvas = DrawService;
