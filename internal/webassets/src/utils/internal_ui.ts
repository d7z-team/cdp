const internalElements = new Set<Element>();

export function markInternalElement<T extends Element>(element: T): T {
    internalElements.add(element);
    return element;
}

export function unmarkInternalElement(element: Element | null | undefined): void {
    if (element) {
        internalElements.delete(element);
    }
}

export function isInternalElement(node: unknown): boolean {
    return node instanceof Element && internalElements.has(node);
}

export function closestInternalElement(node: Node | null): Element | null {
    let current: Node | null = node;
    while (current) {
        if (isInternalElement(current)) {
            return current as Element;
        }
        if (current.parentNode) {
            current = current.parentNode;
            continue;
        }
        const root = current.getRootNode();
        current = root instanceof ShadowRoot ? root.host : null;
    }
    return null;
}

export function listInternalHTMLElements(): HTMLElement[] {
    const result: HTMLElement[] = [];
    for (const element of Array.from(internalElements)) {
        if (!element.isConnected) {
            internalElements.delete(element);
            continue;
        }
        if (element instanceof HTMLElement) {
            result.push(element);
        }
    }
    return result;
}
