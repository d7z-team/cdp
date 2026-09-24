import {shadowRootFor} from "../dom/shadow";
import {getAccessibleName, getElementLabelText} from '../selector/control';
import {closestInternalElement} from '../utils/internal_ui';
import type {SnapshotCaptureRequest, SnapshotDocument, SnapshotNode} from './model';
import {SnapshotRegistry} from './registry';

type FrameElement = HTMLIFrameElement | HTMLFrameElement;
type CaptureFrame = (frame: FrameElement, request: SnapshotCaptureRequest) => Promise<SnapshotDocument>;

const SKIP = new Set(['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'META', 'LINK', 'SOURCE', 'TRACK', 'OBJECT', 'EMBED']);
const CONTAINER_ROLES = new Set(['article', 'banner', 'complementary', 'contentinfo', 'dialog', 'form', 'main', 'navigation', 'region', 'search']);
function composedParent(element: Element): Element | null {
    if (element.parentElement) return element.parentElement;
    const root = element.getRootNode();
    return root instanceof ShadowRoot ? root.host : null;
}

function isDOMVisible(element: Element): boolean {
    if (!element.isConnected) return false;
    let current: Element | null = element;
    while (current) {
        const style = current.ownerDocument.defaultView?.getComputedStyle(current);
        if (!style || style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse' || style.opacity === '0') {
            return false;
        }
        current = composedParent(current);
    }
    return true;
}

function normalizeText(value: string | null | undefined): string {
    return (value || '').replace(/\s+/g, ' ').trim();
}

function explicitOrImplicitRole(element: Element): string {
    const explicit = normalizeText(element.getAttribute('role')).toLowerCase().split(' ')[0];
    if (explicit) return explicit;
    const tag = element.tagName.toLowerCase();
    if (/^h[1-6]$/.test(tag)) return 'heading';
    if (tag === 'button') return 'button';
    if (tag === 'a' && element.hasAttribute('href')) return 'link';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'select') return element.hasAttribute('multiple') ? 'listbox' : 'combobox';
    if (tag === 'option') return 'option';
    if (tag === 'ul' || tag === 'ol') return 'list';
    if (tag === 'li') return 'listitem';
    if (tag === 'nav') return 'navigation';
    if (tag === 'main') return 'main';
    if (tag === 'aside') return 'complementary';
    if (tag === 'header') return 'banner';
    if (tag === 'footer') return 'contentinfo';
    if (tag === 'form') return 'form';
    if (tag === 'dialog') return 'dialog';
    if (tag === 'img') return 'img';
    if (tag === 'input') {
        const type = (element.getAttribute('type') || 'text').toLowerCase();
        if (type === 'checkbox') return 'checkbox';
        if (type === 'radio') return 'radio';
        if (type === 'button' || type === 'submit' || type === 'reset' || type === 'image') return 'button';
        if (type === 'range') return 'slider';
        if (type === 'number') return 'spinbutton';
        if (type === 'search') return 'searchbox';
        if (!['hidden', 'file', 'color'].includes(type)) return 'textbox';
        if (type === 'file') return 'button';
    }
    return '';
}

function snapshotName(element: Element, role: string): string {
    if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) {
        const ownerLabel = element.labels?.[0];
        const clone = ownerLabel?.cloneNode(true);
        if (clone instanceof Element) {
            for (const control of Array.from(clone.querySelectorAll('input, select, textarea, button'))) control.remove();
        }
        const label = normalizeText(clone?.textContent || getElementLabelText(element));
        if (label) return label;
    }
    const accessible = normalizeText(getAccessibleName(element));
    if (accessible) return accessible;
    const label = normalizeText(getElementLabelText(element));
    if (label) return label;
    if (role === 'img') return normalizeText(element.getAttribute('alt'));
    if (CONTAINER_ROLES.has(role)) return normalizeText(element.getAttribute('aria-label') || element.getAttribute('title'));
    if (role === 'list') return '';
    return normalizeText(element.textContent).slice(0, 500);
}

function snapshotStates(element: Element): string[] {
    const states: string[] = [];
    const html = element as HTMLElement;
    const input = element as HTMLInputElement;
    if ('disabled' in html && (html as HTMLButtonElement).disabled || element.getAttribute('aria-disabled') === 'true') states.push('disabled');
    if ('readOnly' in input && input.readOnly) states.push('readonly');
    if ('checked' in input && input.checked) states.push('checked');
    if ('required' in input && input.required) states.push('required');
    for (const attribute of ['expanded', 'pressed', 'selected', 'invalid']) {
        const value = element.getAttribute(`aria-${attribute}`);
        if (value === 'true') states.push(attribute);
        else if (value === 'false' && attribute === 'expanded') states.push('collapsed');
    }
    return states;
}

function snapshotValue(element: Element): string {
    if (element instanceof HTMLInputElement) {
        return element.type === 'password' ? '' : element.value;
    }
    if (element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) return element.value;
    return '';
}

function tableRows(table: HTMLTableElement): string[][] {
    return Array.from(table.rows).map(row => Array.from(row.cells).map(cell => normalizeText(cell.textContent))).filter(row => row.some(Boolean));
}

function childNodes(element: Element): Node[] {
    if (element instanceof HTMLSlotElement) {
        const assigned = element.assignedNodes({flatten: true});
        return assigned.length ? assigned : Array.from(element.childNodes);
    }
    const shadow = shadowRootFor(element);
    if (shadow) return Array.from(shadow.childNodes);
    return Array.from(element.childNodes);
}

export async function captureSnapshot(
    request: SnapshotCaptureRequest,
    runtimeId: string,
    registry: SnapshotRegistry,
    revision: number,
    captureFrame: CaptureFrame,
): Promise<SnapshotDocument> {
    registry.begin(request.snapshot_id);
    const nodes: SnapshotNode[] = [];
    const warnings: string[] = [];
    let nextId = request.next_id;

    const identity = (element: Element): string => `${runtimeId}/${registry.identity(element)}`;

    const visit = async (node: Node, depth: number): Promise<void> => {
        if (node.nodeType === Node.TEXT_NODE) {
            const text = normalizeText(node.textContent);
            if (text) {
                nodes.push({id: 0, kind: 'text', depth, text});
            }
            return;
        }
        if (!(node instanceof Element) || SKIP.has(node.tagName) || closestInternalElement(node) || node.getAttribute('aria-hidden') === 'true' || !isDOMVisible(node)) return;

        if (node instanceof HTMLIFrameElement || node instanceof HTMLFrameElement) {
            const id = nextId++;
            registry.register(request.snapshot_id, id, node);
            nodes.push({
                id, kind: 'frame', depth, role: 'frame',
                name: normalizeText(node.getAttribute('title') || node.getAttribute('name')),
                frame_url: node.getAttribute('src') || '', runtime_id: runtimeId, identity: identity(node),
            });
            const childWindow = node.contentWindow;
            if (childWindow) {
                try {
                    const child = await captureFrame(node, {snapshot_id: request.snapshot_id, next_id: nextId, depth: depth + 1});
                    nodes.push(...child.nodes);
                    nextId = child.next_id;
                    warnings.push(...(child.warnings || []));
                } catch (error) {
                    warnings.push(`frame snapshot unavailable: ${error instanceof Error ? error.message : String(error)}`);
                }
            }
            return;
        }

        if (node instanceof HTMLTableElement) {
            const rows = tableRows(node);
            if (rows.length) {
                const id = nextId++;
                registry.register(request.snapshot_id, id, node);
                nodes.push({
                    id, kind: 'table', depth, role: 'table', name: snapshotName(node, 'table'), rows,
                    runtime_id: runtimeId, identity: identity(node),
                });
            }
            return;
        }
        if (node.tagName === 'PRE') {
            const id = nextId++;
            registry.register(request.snapshot_id, id, node);
            const code = node.querySelector('code');
            const languageClass = Array.from(code?.classList || []).find(value => value.startsWith('language-')) || '';
            nodes.push({
                id, kind: 'code', depth, role: 'code', text: (code?.textContent || node.textContent || '').trimEnd(),
                language: languageClass.replace(/^language-/, ''), runtime_id: runtimeId, identity: identity(node),
            });
            return;
        }

        const role = explicitOrImplicitRole(node);
        const semantic = role !== '' || node.hasAttribute('aria-label') || node.hasAttribute('aria-labelledby');
        let childDepth = depth;
        if (semantic) {
            const id = nextId++;
            registry.register(request.snapshot_id, id, node);
            const level = role === 'heading' ? Number(node.getAttribute('aria-level') || node.tagName.slice(1)) || undefined : undefined;
            nodes.push({
                id, kind: 'semantic', depth, role: role || 'group', name: snapshotName(node, role),
                value: snapshotValue(node) || undefined, level, states: snapshotStates(node), runtime_id: runtimeId,
                identity: identity(node),
            });
            childDepth++;
        }

        const children = childNodes(node);
        if (!semantic && !children.some(child => child.nodeType === Node.ELEMENT_NODE)) {
            const text = normalizeText(children.map(child => child.textContent || '').join(' '));
            if (text) {
                const id = nextId++;
                registry.register(request.snapshot_id, id, node);
                nodes.push({id, kind: 'text', depth, text, runtime_id: runtimeId, identity: identity(node)});
            }
            return;
        }
        for (const child of children) {
            if (semantic && child.nodeType === Node.TEXT_NODE) continue;
            await visit(child, childDepth);
        }
    };

    if (document.body) await visit(document.body, request.depth);
    return {
        id: request.snapshot_id,
        url: location.href,
        title: document.title,
        lang: document.documentElement.lang || undefined,
        revision,
        nodes,
        warnings: warnings.length ? warnings : undefined,
        next_id: nextId,
    };
}
