export type VisualBoxSource =
    | 'client_rect'
    | 'bounding_rect'
    | 'descendant'
    | 'interactive_descendant';

export interface VisualBox {
    x: number;
    y: number;
    width: number;
    height: number;
    source: VisualBoxSource;
    element: Element;
}

export interface VisualBoxOptions {
    includeDescendants?: boolean;
    preferInteractiveDescendant?: boolean;
    maxDepth?: number;
    maxNodes?: number;
    maxViewportRatio?: number;
}

interface DescendantEntry {
    element: Element;
    depth: number;
}

const DEFAULT_MAX_DEPTH = 3;
const DEFAULT_MAX_NODES = 64;
const DEFAULT_MAX_VIEWPORT_RATIO = 0.8;
const SKIPPED_DESCENDANT_TAGS = new Set(['script', 'style', 'template', 'head', 'meta', 'link', 'noscript']);
const INTERACTIVE_ROLES = new Set(['button', 'link', 'menuitem', 'tab', 'checkbox', 'radio', 'switch']);

function composedParent(element: Element): Element | null {
    const parentNode = element.parentNode;
    if (parentNode instanceof ShadowRoot) {
        return parentNode.host;
    }
    return element.parentElement;
}

export function hasBasicVisualVisibility(element: Element | null): boolean {
    if (!element || !element.isConnected) {
        return false;
    }
    let current: Element | null = element;
    while (current) {
        const view = current.ownerDocument?.defaultView;
        if (!view) {
            return false;
        }
        const style = view.getComputedStyle(current);
        if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') {
            return false;
        }
        const opacity = Number.parseFloat(style.opacity || '1');
        if (Number.isFinite(opacity) && opacity <= 0) {
            return false;
        }
        current = composedParent(current);
    }
    return true;
}

function boxFromRect(rect: DOMRect, source: VisualBoxSource, element: Element): VisualBox | null {
    if (!Number.isFinite(rect.left)
        || !Number.isFinite(rect.top)
        || !Number.isFinite(rect.width)
        || !Number.isFinite(rect.height)
        || rect.width <= 0
        || rect.height <= 0) {
        return null;
    }
    return {
        x: rect.left,
        y: rect.top,
        width: rect.width,
        height: rect.height,
        source,
        element,
    };
}

function ownVisualBox(element: Element): VisualBox | null {
    const rects = element.getClientRects();
    for (let i = 0; i < rects.length; i++) {
        const box = boxFromRect(rects[i], 'client_rect', element);
        if (box) {
            return box;
        }
    }
    return boxFromRect(element.getBoundingClientRect(), 'bounding_rect', element);
}

function isInteractiveElement(element: Element): boolean {
    if (element instanceof HTMLButtonElement
        || element instanceof HTMLInputElement
        || element instanceof HTMLSelectElement
        || element instanceof HTMLTextAreaElement
        || element instanceof HTMLLabelElement) {
        return true;
    }
    if (element instanceof HTMLAnchorElement && !!element.getAttribute('href')) {
        return true;
    }
    const role = element.getAttribute('role')?.trim().toLowerCase();
    if (role && INTERACTIVE_ROLES.has(role)) {
        return true;
    }
    const tabindex = element.getAttribute('tabindex');
    if (tabindex !== null && Number.parseInt(tabindex, 10) >= 0) {
        return true;
    }
    return element instanceof HTMLElement && element.isContentEditable;
}

function enqueueChildren(queue: DescendantEntry[], element: Element, depth: number): void {
    for (const child of Array.from(element.children)) {
        queue.push({element: child, depth});
    }
}

function unionBoxes(target: Element, boxes: VisualBox[]): VisualBox | null {
    if (boxes.length === 0) {
        return null;
    }
    let left = Number.POSITIVE_INFINITY;
    let top = Number.POSITIVE_INFINITY;
    let right = Number.NEGATIVE_INFINITY;
    let bottom = Number.NEGATIVE_INFINITY;
    for (const box of boxes) {
        left = Math.min(left, box.x);
        top = Math.min(top, box.y);
        right = Math.max(right, box.x + box.width);
        bottom = Math.max(bottom, box.y + box.height);
    }
    const width = right - left;
    const height = bottom - top;
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
        return null;
    }
    return {x: left, y: top, width, height, source: 'descendant', element: target};
}

function exceedsViewportRatio(element: Element, box: VisualBox, maxViewportRatio: number): boolean {
    if (!Number.isFinite(maxViewportRatio) || maxViewportRatio <= 0) {
        return false;
    }
    const view = element.ownerDocument?.defaultView;
    if (!view) {
        return false;
    }
    const viewportArea = Math.max(1, view.innerWidth * view.innerHeight);
    return box.width * box.height > viewportArea * maxViewportRatio;
}

function descendantVisualBox(element: Element, options: Required<VisualBoxOptions>): VisualBox | null {
    const queue: DescendantEntry[] = [];
    const boxes: VisualBox[] = [];
    let visited = 0;
    enqueueChildren(queue, element, 1);
    while (queue.length > 0 && visited < options.maxNodes) {
        const entry = queue.shift();
        if (!entry) {
            break;
        }
        visited++;
        const tagName = entry.element.tagName.toLowerCase();
        if (SKIPPED_DESCENDANT_TAGS.has(tagName) || !hasBasicVisualVisibility(entry.element)) {
            continue;
        }
        const ownBox = ownVisualBox(entry.element);
        if (ownBox) {
            if (options.preferInteractiveDescendant && isInteractiveElement(entry.element)) {
                if (!exceedsViewportRatio(element, ownBox, options.maxViewportRatio)) {
                    return {...ownBox, source: 'interactive_descendant'};
                }
            }
            boxes.push({...ownBox, source: 'descendant'});
        }
        if (entry.depth < options.maxDepth) {
            enqueueChildren(queue, entry.element, entry.depth + 1);
        }
    }
    const union = unionBoxes(element, boxes);
    if (!union || exceedsViewportRatio(element, union, options.maxViewportRatio)) {
        return null;
    }
    return union;
}

export function visualBoxForElement(element: Element, options: VisualBoxOptions = {}): VisualBox | null {
    if (!hasBasicVisualVisibility(element)) {
        return null;
    }
    const selfBox = ownVisualBox(element);
    if (selfBox) {
        return selfBox;
    }
    const resolved: Required<VisualBoxOptions> = {
        includeDescendants: options.includeDescendants ?? true,
        preferInteractiveDescendant: options.preferInteractiveDescendant ?? false,
        maxDepth: Math.max(0, Math.floor(options.maxDepth ?? DEFAULT_MAX_DEPTH)),
        maxNodes: Math.max(0, Math.floor(options.maxNodes ?? DEFAULT_MAX_NODES)),
        maxViewportRatio: options.maxViewportRatio ?? DEFAULT_MAX_VIEWPORT_RATIO,
    };
    if (!resolved.includeDescendants || resolved.maxDepth <= 0 || resolved.maxNodes <= 0) {
        return null;
    }
    return descendantVisualBox(element, resolved);
}
