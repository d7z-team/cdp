export interface BrowserErrorNode {
    kind?: string;
    message?: string;
    detail?: string;
    op?: string;
    selector?: string;
    runtimeId?: string;
    frameSummary?: string;
    data?: Record<string, unknown>;
    cause?: BrowserErrorNode;
}

const TEXT_LIMIT = 512;
const DATA_LIMIT = 24;
const ARRAY_LIMIT = 8;
const CAUSE_DEPTH_LIMIT = 8;

function trimText(value: string | undefined): string | undefined {
    const text = (value || '').trim();
    if (!text) {
        return undefined;
    }
    return text.length > TEXT_LIMIT ? text.slice(0, TEXT_LIMIT) + '...(truncated)' : text;
}

function sanitizeValue(value: unknown, depth: number): unknown {
    if (value === null || value === undefined) {
        return undefined;
    }
    if (typeof value === 'string') {
        return trimText(value);
    }
    if (typeof value === 'number' || typeof value === 'boolean') {
        return value;
    }
    if (Array.isArray(value)) {
        return value.slice(0, ARRAY_LIMIT)
            .map((item) => sanitizeValue(item, depth + 1))
            .filter((item) => item !== undefined);
    }
    if (typeof value === 'object' && depth < CAUSE_DEPTH_LIMIT) {
        return sanitizeData(value as Record<string, unknown>, depth + 1);
    }
    return undefined;
}

function sanitizeData(data: Record<string, unknown> | undefined, depth: number): Record<string, unknown> | undefined {
    if (!data || depth >= CAUSE_DEPTH_LIMIT) {
        return undefined;
    }
    const out: Record<string, unknown> = {};
    for (const [rawKey, value] of Object.entries(data).slice(0, DATA_LIMIT)) {
        const key = trimText(rawKey);
        const clean = sanitizeValue(value, depth + 1);
        if (key && clean !== undefined) {
            out[key] = clean;
        }
    }
    return Object.keys(out).length > 0 ? out : undefined;
}

export function makeBrowserErrorNode(input: BrowserErrorNode, depth = 0): BrowserErrorNode {
    const node: BrowserErrorNode = {
        op: trimText(input.op),
        kind: trimText(input.kind),
        message: trimText(input.message),
        detail: trimText(input.detail),
        selector: trimText(input.selector),
        runtimeId: trimText(input.runtimeId),
        frameSummary: trimText(input.frameSummary),
        data: sanitizeData(input.data, depth),
    };
    if (input.cause && depth + 1 < CAUSE_DEPTH_LIMIT) {
        node.cause = makeBrowserErrorNode(input.cause, depth + 1);
    }
    return node;
}

export function withBrowserErrorFrameSummary(error: BrowserErrorNode | undefined, frameSummary: string): BrowserErrorNode | undefined {
    if (!error) {
        return undefined;
    }
    return makeBrowserErrorNode({
        ...error,
        frameSummary: error.frameSummary || frameSummary,
    });
}
