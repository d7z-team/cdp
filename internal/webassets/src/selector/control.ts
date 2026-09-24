import {hasInternalFixedIDPrefix} from "../utils/internal_ids";

function normalizeText(text: string): string {
    return text.replace(/\s+/g, " ").trim();
}

export function getAccessibleName(element: Element): string {
    const ariaLabelledBy = element.getAttribute('aria-labelledby')?.trim();
    if (ariaLabelledBy) {
        const doc = element.ownerDocument || document;
        const ids = ariaLabelledBy.split(/\s+/).filter(Boolean);
        const text = normalizeText(ids
            .map((id) => doc.getElementById(id)?.textContent || '')
            .join(' '));
        if (text) return text;
    }

    const ariaLabel = element.getAttribute('aria-label')?.trim();
    if (ariaLabel) return normalizeText(ariaLabel);

    const title = element.getAttribute('title')?.trim();
    if (title) return normalizeText(title);

    const text = getElementStableText(element);
    if (text) return text;

    return '';
}

export function getElementStableText(element: Element): string {
    const text = normalizeText(
        element instanceof HTMLInputElement && ['button', 'submit', 'reset'].includes((element.type || '').toLowerCase())
            ? element.value
            : (element.textContent || '')
    );
    return text;
}

export function getElementLabelText(element: Element): string {
    if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) {
        const labels = (element as HTMLInputElement & { labels?: NodeListOf<HTMLLabelElement> }).labels;
        if (labels && labels.length > 0) {
            const text = normalizeText(labels[0]?.textContent || '');
            if (text) return text;
        }
    }

    if (element.id && isStableId(element.id)) {
        const label = (element.ownerDocument || document).querySelector(`label[for="${CSS.escape(element.id)}"]`);
        const text = normalizeText(label?.textContent || '');
        if (text) return text;
    }

    const parentLabel = (element as HTMLElement).closest?.('label');
    if (parentLabel) {
        const clone = parentLabel.cloneNode(true) as HTMLElement;
        const control = clone.querySelector('input, select, textarea');
        if (control) control.remove();
        const text = normalizeText(clone.textContent || '');
        if (text) return text;
    }

    const ariaLabel = element.getAttribute('aria-label')?.trim();
    if (ariaLabel) return normalizeText(ariaLabel);

    const title = element.getAttribute('title')?.trim();
    if (title) return normalizeText(title);

    return '';
}

function isStableId(id: string): boolean {
    if (!id || id.length > 80) return false;
    if (/^[:_]/.test(id)) return false;
    if (hasInternalFixedIDPrefix(id)) return false;
    if (/^(headlessui|react-select|radix)-/.test(id)) return false;
    if (/[A-Fa-f0-9]{8,}/.test(id)) return false;
    if (/\d{4,}/.test(id)) return false;
    if (/(?:[-_:])\d{5,}$/.test(id)) return false;
    return true;
}
