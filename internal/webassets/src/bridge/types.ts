export interface WireMessage {
    type: '__cdp_bridge__';
    direction: 'event' | 'request' | 'response';
    channel: string;
    requestId?: string;
    srcRuntimeId: string;
    dstRuntimeId?: string;
    payload: unknown;
}

export const Channel = {
    SELECTOR_QUERY:    'selector.query',
    SELECTOR_RECTS:    'selector.rects',
    SELECTOR_PROJECT_RECTS: 'selector.project_rects',
    SELECTOR_QUERY_DISPOSE: 'selector.query.dispose',
    HIGHLIGHT_FRAME_RECTS: 'highlight.frame_rects',
    RUNTIME_READY:     'runtime.ready',
    ACTIONABILITY:     'actionability',
    SCREENSHOT_PROJECT_TILE: 'screenshot.project_tile',
    SNAPSHOT_CAPTURE:  'snapshot.capture',
    SNAPSHOT_QUIET:    'snapshot.quiet',
    CANVAS_CMD:        'canvas.cmd',
} as const;

export type ChannelName = (typeof Channel)[keyof typeof Channel];

export interface PostOptions {
    target?: Window | 'parent' | 'top';
}

export type Unsubscribe = () => void;

const BRIDGE_TYPE = '__cdp_bridge__';
export { BRIDGE_TYPE as BRIDGE_MESSAGE_TYPE };

export const DEFAULT_REQUEST_TIMEOUT_MS = 1500;
