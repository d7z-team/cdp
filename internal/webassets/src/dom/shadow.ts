// Author roots resolved by CDP retain their original mode and identity. Weak
// keys let detached hosts and their roots be collected with the document.
const authorRoots = new WeakMap<Element, ShadowRoot>();

export function registerShadowRoots(roots: ShadowRoot[]): void {
    for (const root of roots) {
        if (root.host && root.host.ownerDocument === document) authorRoots.set(root.host, root);
    }
}

export function shadowRootFor(element: Element): ShadowRoot | null {
    return element.shadowRoot ?? authorRoots.get(element) ?? null;
}

export function* authorShadowRoots(scope: Document | ShadowRoot | Element = document): Generator<ShadowRoot> {
    for (const element of scope.querySelectorAll('*')) {
        const root = shadowRootFor(element);
        if (root) {
            yield root;
            yield* authorShadowRoots(root);
        }
    }
}

export function frameElements(): Array<HTMLIFrameElement | HTMLFrameElement> {
    const frames: Array<HTMLIFrameElement | HTMLFrameElement> = [];
    for (const root of [document, ...authorShadowRoots()]) {
        frames.push(...root.querySelectorAll<HTMLIFrameElement | HTMLFrameElement>('iframe, frame'));
    }
    return frames;
}

export function deepElementFromPoint(doc: Document, x: number, y: number): Element | null {
    let hit = doc.elementFromPoint(x, y);
    for (let depth = 0; hit && depth < 64; depth++) {
        const shadow = shadowRootFor(hit);
        if (!shadow) break;
        const nested = shadow.elementFromPoint(x, y);
        if (!nested || nested === hit) break;
        hit = nested;
    }
    return hit;
}
