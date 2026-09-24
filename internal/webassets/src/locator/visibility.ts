import {composedParentElement} from "../utils/dom";

export interface VisibilityDiagnostic {
    visible: boolean;
    code: 'ok' | 'not_found' | 'detached' | 'display_none' | 'visibility_hidden' | 'opacity_zero' | 'zero_size';
    detail: string;
    culprit: Element | null;
    rect: DOMRect | null;
}

function ownerWindow(element: Element): Window | null {
    return element.ownerDocument?.defaultView || null;
}

const STABLE_SAMPLE_INTERVAL_MS = 120;
const FAST_STABLE_SAMPLE_INTERVAL_MS = 16;
const STABLE_TIMEOUT_MS = 1200;
const FAST_STABLE_TIMEOUT_MS = 600;
const STABLE_POSITION_TOLERANCE_PX = 1;
const STABLE_SIZE_TOLERANCE_PX = 1;
const STABILITY_SAMPLE_LIMIT = 3;
const STABLE_REQUIRED_CONSECUTIVE_MATCHES = 2;

export interface StabilityOptions {
    mode?: 'strict' | 'fast';
    sampleIntervalMs?: number;
    stabilityTimeoutMs?: number;
}

export interface RectStabilityDelta {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface StabilityDiagnostic {
    stable: boolean;
    kind: 'stable' | 'detached' | 'unavailable' | 'timeout';
    rect?: DOMRect;
    samples: DOMRect[];
    maxDelta: RectStabilityDelta;
    elapsedMs: number;
}


export function firstNonEmptyClientRect(element: Element): DOMRect | null {
    const rects = element.getClientRects();
    for (const rect of Array.from(rects)) {
        if (rect.width > 0 && rect.height > 0) {
            return rect;
        }
    }
    const bounds = element.getBoundingClientRect();
    if (bounds.width > 0 && bounds.height > 0) {
        return bounds;
    }
    return null;
}

export function diagnoseVisibility(element: Element | null): VisibilityDiagnostic {
    if (!element) {
        return { visible: false, code: 'not_found', detail: 'element reference is null', culprit: null, rect: null };
    }
    if (!element.isConnected) {
        return { visible: false, code: 'detached', detail: 'element is detached from DOM', culprit: element, rect: null };
    }

    let current: Element | null = element;
    while (current) {
        const view = ownerWindow(current);
        const style = view?.getComputedStyle(current);
        if (!style) {
            return { visible: false, code: 'detached', detail: 'unable to resolve computed style', culprit: current, rect: null };
        }
        if (style.display === 'none') {
            return { visible: false, code: 'display_none', detail: 'display is none', culprit: current, rect: null };
        }
        if (style.visibility === 'hidden' || style.visibility === 'collapse') {
            return { visible: false, code: 'visibility_hidden', detail: `visibility is ${style.visibility}`, culprit: current, rect: null };
        }
        if (style.opacity === '0') {
            return { visible: false, code: 'opacity_zero', detail: 'opacity is 0', culprit: current, rect: null };
        }
        current = composedParentElement(current);
    }

    const rect = firstNonEmptyClientRect(element);
    if (!rect) {
        const bounds = element.getBoundingClientRect();
        return {
            visible: false,
            code: 'zero_size',
            detail: `element has no non-empty client rects (bounding box ${bounds.width.toFixed(1)}x${bounds.height.toFixed(1)})`,
            culprit: element,
            rect: bounds,
        };
    }

    return { visible: true, code: 'ok', detail: '', culprit: element, rect };
}

export function isVisible(element: Element | null): boolean {
    return diagnoseVisibility(element).visible;
}

function stableSampleInterval(options?: StabilityOptions): number {
    if (typeof options?.sampleIntervalMs === 'number' && Number.isFinite(options.sampleIntervalMs) && options.sampleIntervalMs >= 0) {
        return options.sampleIntervalMs;
    }
    return options?.mode === 'fast' ? FAST_STABLE_SAMPLE_INTERVAL_MS : STABLE_SAMPLE_INTERVAL_MS;
}

export function diagnoseStability(element: Element, options?: StabilityOptions): Promise<StabilityDiagnostic> {
    return new Promise((resolve) => {
        const emptyDelta = {x: 0, y: 0, width: 0, height: 0};
        if (!element || !element.isConnected) {
            resolve({stable: false, kind: 'detached', samples: [], maxDelta: emptyDelta, elapsedMs: 0});
            return;
        }
        const view = ownerWindow(element);
        if (!view) {
            resolve({stable: false, kind: 'unavailable', samples: [], maxDelta: emptyDelta, elapsedMs: 0});
            return;
        }
        const sampleIntervalMs = stableSampleInterval(options);
        const configuredTimeout = options?.stabilityTimeoutMs;
        const timeoutMs = typeof configuredTimeout === 'number' && Number.isFinite(configuredTimeout) && configuredTimeout >= sampleIntervalMs
            ? configuredTimeout
            : options?.mode === 'fast' ? FAST_STABLE_TIMEOUT_MS : STABLE_TIMEOUT_MS;
        const startedAt = view.performance.now();
        const snapshots = [element.getBoundingClientRect()];
        const maxDelta = {x: 0, y: 0, width: 0, height: 0};
        let consecutiveMatches = 0;
        const takeSample = () => {
            const elapsedMs = view.performance.now() - startedAt;
            if (!element.isConnected) {
                resolve({
                    stable: false,
                    kind: 'detached',
                    rect: snapshots[snapshots.length - 1],
                    samples: snapshots.slice(-STABILITY_SAMPLE_LIMIT),
                    maxDelta,
                    elapsedMs,
                });
                return;
            }
            const previous = snapshots[snapshots.length - 1];
            const current = element.getBoundingClientRect();
            const delta = {
                x: Math.abs(previous.left - current.left),
                y: Math.abs(previous.top - current.top),
                width: Math.abs(previous.width - current.width),
                height: Math.abs(previous.height - current.height),
            };
            maxDelta.x = Math.max(maxDelta.x, delta.x);
            maxDelta.y = Math.max(maxDelta.y, delta.y);
            maxDelta.width = Math.max(maxDelta.width, delta.width);
            maxDelta.height = Math.max(maxDelta.height, delta.height);
            snapshots.push(current);
            if (
                delta.x <= STABLE_POSITION_TOLERANCE_PX &&
                delta.y <= STABLE_POSITION_TOLERANCE_PX &&
                delta.width <= STABLE_SIZE_TOLERANCE_PX &&
                delta.height <= STABLE_SIZE_TOLERANCE_PX
            ) {
                consecutiveMatches += 1;
                if (consecutiveMatches >= STABLE_REQUIRED_CONSECUTIVE_MATCHES) {
                    resolve({
                        stable: true,
                        kind: 'stable',
                        rect: current,
                        samples: snapshots.slice(-STABILITY_SAMPLE_LIMIT),
                        maxDelta,
                        elapsedMs,
                    });
                    return;
                }
            } else {
                consecutiveMatches = 0;
            }
            if (elapsedMs >= timeoutMs) {
                resolve({
                    stable: false,
                    kind: 'timeout',
                    rect: current,
                    samples: snapshots.slice(-STABILITY_SAMPLE_LIMIT),
                    maxDelta,
                    elapsedMs,
                });
                return;
            }
            view.setTimeout(takeSample, Math.min(sampleIntervalMs, timeoutMs - elapsedMs));
        };
        view.setTimeout(takeSample, sampleIntervalMs);
    });
}

export async function isStable(element: Element, options?: StabilityOptions): Promise<boolean> {
    return (await diagnoseStability(element, options)).stable;
}

function isNaturallyFocusable(element: Element): boolean {
    if (element instanceof HTMLAnchorElement) {
        return element.hasAttribute('href');
    }
    if (element instanceof HTMLButtonElement ||
        element instanceof HTMLInputElement ||
        element instanceof HTMLSelectElement ||
        element instanceof HTMLTextAreaElement ||
        element instanceof HTMLIFrameElement) {
        return true;
    }
    if (element instanceof HTMLAudioElement || element instanceof HTMLVideoElement) {
        return element.hasAttribute('controls');
    }
    return false;
}

function isFocusableElement(element: Element): boolean {
    if (element instanceof HTMLElement) {
        if (element.tabIndex >= 0) {
            return true;
        }
        if (element.isContentEditable) {
            return true;
        }
    }
    return isNaturallyFocusable(element);
}

function hasInheritedAriaDisabled(element: Element): boolean {
    if (!isFocusableElement(element)) {
        return false;
    }
    let current = composedParentElement(element);
    while (current) {
        if (current.getAttribute('aria-disabled') === 'true') {
            return true;
        }
        current = composedParentElement(current);
    }
    return false;
}

export function supportsNativeDisabled(element: Element | null): boolean {
    if (!element) return false;
    return element instanceof HTMLButtonElement ||
        element instanceof HTMLInputElement ||
        element instanceof HTMLSelectElement ||
        element instanceof HTMLTextAreaElement ||
        element instanceof HTMLOptionElement ||
        element instanceof HTMLOptGroupElement ||
        element instanceof HTMLFieldSetElement;
}

export function isEnabled(element: Element): boolean {
    if (!element) return false;
    if (element.getAttribute('aria-disabled') === 'true' || hasInheritedAriaDisabled(element)) {
        return false;
    }
    if (!supportsNativeDisabled(element)) {
        return true;
    }
    if (typeof (element as Element).matches === 'function') {
        return !(element as Element).matches(':disabled');
    }
    return !('disabled' in element && (element as HTMLButtonElement).disabled);
}
