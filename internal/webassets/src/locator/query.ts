// Basic implementation of semantic queries

function shouldStop(results: Element[], limit?: number): boolean {
    return typeof limit === 'number' && limit > 0 && results.length >= limit;
}

function hasNodeType(value: unknown, nodeType: number): boolean {
    return !!value && typeof value === 'object' && 'nodeType' in value && (value as {nodeType?: unknown}).nodeType === nodeType;
}

function isDocumentNode(value: unknown): value is Document {
    return hasNodeType(value, 9);
}

function isElementNode(value: unknown): value is Element {
    return hasNodeType(value, 1);
}

function isInputElement(value: unknown): value is HTMLInputElement {
    return isElementNode(value) && value.tagName.toLowerCase() === 'input';
}

function isTextareaElement(value: unknown): value is HTMLTextAreaElement {
    return isElementNode(value) && value.tagName.toLowerCase() === 'textarea';
}

function getQueryDocument(root: Element | Document | ShadowRoot): Document {
    if (isDocumentNode(root)) {
        return root;
    }
    return root.ownerDocument || document;
}

function normalizeSearchText(text: string): string {
    return text.replace(/\s+/g, ' ').trim().toLowerCase();
}

function getElementSearchText(el: Element): string {
    if (isInputElement(el)) {
        const type = (el.type || '').toLowerCase();
        if (['submit', 'button', 'text', 'search', 'email', 'tel', 'url'].includes(type)) {
            return normalizeSearchText(el.value || '');
        }
    }
    if (isTextareaElement(el)) {
        return normalizeSearchText(el.value || '');
    }
    return normalizeSearchText(el.textContent || '');
}

function pushMinimalTextMatch(results: Element[], seen: Set<Element>, element: Element): void {
    if (seen.has(element)) return;
    for (let i = results.length - 1; i >= 0; i--) {
        const existing = results[i];
        if (existing.contains(element)) {
            results.splice(i, 1);
            seen.delete(existing);
            continue;
        }
        if (element.contains(existing)) {
            return;
        }
    }
    results.push(element);
    seen.add(element);
}

function matchElementsByText(
    root: Element | Document | ShadowRoot,
    text: string,
    exact: boolean,
    limit: number | undefined,
    minimal: boolean,
): Element[] {
    const results: Element[] = [];
    const seen = new Set<Element>();
    const normalizedText = normalizeSearchText(text);
    if (!normalizedText) {
        return results;
    }

    const pushMatch = (element: Element) => {
        const content = getElementSearchText(element);
        if (!content) return;
        const matched = exact ? content === normalizedText : content.includes(normalizedText);
        if (!matched) return;
        if (minimal) {
            pushMinimalTextMatch(results, seen, element);
            return;
        }
        if (!seen.has(element)) {
            results.push(element);
            seen.add(element);
        }
    };

    if (isElementNode(root)) {
        pushMatch(root);
        if (shouldStop(results, limit)) {
            return results;
        }
    }

    for (const element of root.querySelectorAll('*')) {
        pushMatch(element);
        if (shouldStop(results, limit)) {
            break;
        }
    }

    return results;
}

export function getByText(root: Element | Document | ShadowRoot, text: string, exact: boolean = false, limit?: number): Element[] {
    return matchElementsByText(root, text, exact, limit, true);
}

export function getByHasText(root: Element | Document | ShadowRoot, text: string, exact: boolean = false, limit?: number): Element[] {
    return matchElementsByText(root, text, exact, limit, false);
}

function getAccessibleName(el: Element, queryDocument: Document): string {
    let accName = '';

    accName = el.getAttribute('aria-label') || '';

    if (!accName) {
        const labelledBy = el.getAttribute('aria-labelledby');
        if (labelledBy) {
            const labelEl = queryDocument.getElementById(labelledBy);
            if (labelEl) accName = labelEl.textContent || '';
        }
    }

    if (!accName && 'labels' in el && (el as HTMLInputElement).labels && (el as HTMLInputElement).labels!.length > 0) {
        accName = (el as HTMLInputElement).labels![0].textContent || '';
    }

    if (!accName) {
        accName = el.textContent || '';
    }

    if (!accName && isInputElement(el) && (el.type === 'submit' || el.type === 'button')) {
        accName = el.value || '';
    }

    return accName.trim().toLowerCase();
}

export function getByRole(root: Element | Document | ShadowRoot, role: string, name?: string, limit?: number): Element[] {
    // Simplified role resolution: look at semantic tags and role attributes
    const queryDocument = getQueryDocument(root);
    const roleMap: Record<string, string> = {
        'button': 'button, input[type="button"], input[type="submit"], [role="button"]',
        'checkbox': 'input[type="checkbox"], [role="checkbox"]',
        'radio': 'input[type="radio"], [role="radio"]',
        'heading': 'h1, h2, h3, h4, h5, h6, [role="heading"]',
        'link': 'a[href], [role="link"]',
        'textbox': 'input[type="text"], input:not([type]), textarea, [role="textbox"]',
        'combobox': 'select, [role="combobox"]',
        'listbox': 'select[multiple], [role="listbox"]',
        'table': 'table, [role="table"], [role="grid"]',
        'row': 'tr, [role="row"]',
        'cell': 'td, th, [role="cell"], [role="gridcell"], [role="columnheader"], [role="rowheader"]',
        'list': 'ul, ol, [role="list"]',
        'listitem': 'li, [role="listitem"]',
        'form': 'form, [role="form"]',
        'option': 'option, [role="option"]',
        'img': 'img, svg, [role="img"]',
        'dialog': 'dialog, [role="dialog"]',
        // add more as needed
    };

    const selector = roleMap[role.toLowerCase()] || `[role="${role}"]`;
    const results: Element[] = [];
    const lowerName = name?.toLowerCase();

    for (const el of root.querySelectorAll(selector)) {
        if (lowerName) {
            const accName = getAccessibleName(el, queryDocument);
            if (!accName.includes(lowerName)) {
                continue;
            }
        }
        results.push(el);
        if (shouldStop(results, limit)) {
            break;
        }
    }

    return results;
}

export function getByPlaceholder(root: Element | Document | ShadowRoot, text: string, exact: boolean = false, limit?: number): Element[] {
    const inputs = root.querySelectorAll('[placeholder]');
    const results: Element[] = [];
    const lowerText = text.toLowerCase();

    for (const input of inputs) {
        const placeholder = input.getAttribute('placeholder')?.trim().toLowerCase() || '';
        if (exact) {
            if (placeholder === lowerText) results.push(input);
        } else {
            if (placeholder.includes(lowerText)) results.push(input);
        }
        if (shouldStop(results, limit)) {
            break;
        }
    }

    return results;
}

export function getByLabel(root: Element | Document | ShadowRoot, text: string, exact: boolean = false, limit?: number): Element[] {
    const results: Element[] = [];
    const lowerText = text.toLowerCase();
    const queryDocument = getQueryDocument(root);

    // 1. Find <label> elements with matching text
    const labels = Array.from(root.querySelectorAll('label'));
    for (const label of labels) {
        const labelText = label.textContent?.trim().toLowerCase() || '';
        if (exact ? labelText === lowerText : labelText.includes(lowerText)) {
            // Find the element associated with this label
            let control = 'control' in label ? (label as HTMLLabelElement).control : null;
            if (!control) {
                const forAttr = label.getAttribute('for');
                if (forAttr) {
                    control = queryDocument.getElementById(forAttr);
                }
            }
            if (control && !results.includes(control)) {
                results.push(control);
            }
        }
        if (shouldStop(results, limit)) {
            return results;
        }
    }

    // 2. Find elements with aria-label
    const ariaLabels = Array.from(root.querySelectorAll('[aria-label]'));
    for (const el of ariaLabels) {
        const val = el.getAttribute('aria-label')?.trim().toLowerCase() || '';
        if (exact ? val === lowerText : val.includes(lowerText)) {
            if (!results.includes(el)) {
                results.push(el);
            }
        }
        if (shouldStop(results, limit)) {
            break;
        }
    }

    return results;
}

export function getByTestId(root: Element | Document | ShadowRoot, testId: string, limit?: number): Element[] {
    // Escape quotes in testId just in case
    const safeId = testId.replace(/"/g, '\\"');
    const elements = root.querySelectorAll(
        `[data-testid="${safeId}"], [test-id="${safeId}"], [data-test="${safeId}"], [data-cy="${safeId}"], [data-qa="${safeId}"]`
    );
    const results: Element[] = [];
    for (const el of elements) {
        results.push(el);
        if (shouldStop(results, limit)) {
            break;
        }
    }
    return results;
}
