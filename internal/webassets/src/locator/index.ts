import {isFrameElement} from "../utils/frame_element";
import {deepElementFromPoint} from "../dom/shadow";
import {composedParentElement} from "../utils/dom";

import { diagnoseStability, diagnoseVisibility, firstNonEmptyClientRect, isVisible, isStable, isEnabled, supportsNativeDisabled } from './visibility';
import type { StabilityOptions, VisibilityDiagnostic } from './visibility';

export interface RectSnapshot {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface FrameSegmentDiagnostic {
    frameElement: string;
    summary: string;
    detail: string;
    rect?: RectSnapshot;
}

export interface ActionabilityDiagnostic {
    actionable: boolean;
    kind: 'ok' | 'not_found' | 'detached' | 'unavailable' | 'not_visible' | 'disabled' | 'unstable' | 'outside_viewport' | 'obscured' | 'delegated_clickable' | 'cross_origin_unreachable' | 'frame_chain_blocked';
    summary: string;
    detail: string;
    element: string;
    culprit?: string;
    hitTarget?: string;
    hitTargetRole?: 'self' | 'descendant' | 'delegate' | 'obstructor';
    delegateReason?: string;
    localRect?: RectSnapshot;
    topRect?: RectSnapshot;
    centerX?: number;
    centerY?: number;
    retriable?: boolean;
    retryAction?: 'hover_priming';
    retryPoint?: {x: number; y: number};
    retryDelayMs?: number;
    stability?: {
        kind: 'stable' | 'detached' | 'unavailable' | 'timeout';
        elapsedMs: number;
        maxDelta: RectSnapshot;
        samples: RectSnapshot[];
    };
    frameChain: FrameSegmentDiagnostic[];
}

export type ActionabilityPurpose = 'click' | 'hover' | 'input' | 'press' | 'drag';

export interface ActionabilityOptions extends StabilityOptions {
    purpose?: ActionabilityPurpose;
    allowOutsideViewport?: boolean;
    scrollIntoView?: boolean;
    repositionObscured?: boolean;
    skipStability?: boolean;
}

export interface CdpLocator {
    isVisible(el: Element | null): boolean;
    isStable(el: Element, options?: ActionabilityOptions): Promise<boolean>;
    isEnabled(el: Element): boolean;
    checkActionability(el: Element | null, options?: ActionabilityOptions): Promise<ActionabilityDiagnostic>;
}

function rectSnapshotFromDOMRect(rect: DOMRect | null | undefined): RectSnapshot | undefined {
    if (!rect) {
        return undefined;
    }
    return {
        x: rect.left,
        y: rect.top,
        width: rect.width,
        height: rect.height,
    };
}

function ownerViewport(el: Element): { width: number; height: number } {
    const view = el.ownerDocument?.defaultView;
    return {
        width: view?.innerWidth ?? 0,
        height: view?.innerHeight ?? 0,
    };
}


function isEditableInputType(type: string | null): boolean {
    if (!type) {
        return true;
    }
    switch (type.toLowerCase()) {
        case 'button':
        case 'checkbox':
        case 'color':
        case 'file':
        case 'hidden':
        case 'image':
        case 'radio':
        case 'range':
        case 'reset':
        case 'submit':
            return false;
        default:
            return true;
    }
}

function elementView(element: Element): (Window & typeof globalThis) | null {
    return (element.ownerDocument?.defaultView || null) as (Window & typeof globalThis) | null;
}

function isEditableElement(element: Element): boolean {
    const view = elementView(element);
    if (view && element instanceof view.HTMLInputElement) {
        return isEditableInputType(element.getAttribute('type'));
    }
    return !!view && (
        element instanceof view.HTMLTextAreaElement ||
        (element instanceof view.HTMLElement && (element as HTMLElement).isContentEditable)
    );
}

function hasUsableGeometry(element: Element): boolean {
    const rect = firstNonEmptyClientRect(element) || element.getBoundingClientRect();
    return !!rect && rect.width > 0 && rect.height > 0;
}

function isVisibleEditableEndpoint(element: Element | null): element is Element {
    return !!element && isEditableElement(element) && isVisible(element) && hasUsableGeometry(element);
}

function rootContains(root: Element, candidate: Element | null): boolean {
    return !!candidate && (root === candidate || root.contains(candidate));
}

function firstVisibleEditable(root: ParentNode, selector: string): Element | null {
    for (const candidate of Array.from(root.querySelectorAll(selector))) {
        if (isVisibleEditableEndpoint(candidate)) {
            return candidate;
        }
    }
    return null;
}

function sameOriginFrameDocument(frame: HTMLIFrameElement | HTMLFrameElement): Document | null {
    try {
        return frame.contentDocument || frame.contentWindow?.document || null;
    } catch {
        return null;
    }
}

function firstFrameEditable(root: ParentNode, selector: string): Element | null {
    for (const candidate of Array.from(root.querySelectorAll(selector))) {
        if (candidate.isConnected && isEditableElement(candidate)) {
            return candidate;
        }
    }
    return null;
}

const EDITOR_HOST_SELECTOR = [
    '.monaco-editor',
    '.CodeMirror',
    '.cm-editor',
    '.ace_editor',
    '[role="textbox"]',
    '[aria-multiline="true"]',
].join(', ');
const EDITOR_LIKE_SELECTOR = [
    EDITOR_HOST_SELECTOR,
    '.view-lines',
    '.monaco-scrollable-element',
    '.inputarea',
    '.cm-content',
    '.ace_text-input',
].join(', ');

function closestVisibleEditorLikeContainer(root: Element): Element | null {
    const closestHost = typeof root.closest === 'function' ? root.closest(EDITOR_HOST_SELECTOR) : null;
    if (closestHost && closestHost.isConnected && hasUsableGeometry(closestHost) && isVisible(closestHost)) {
        return closestHost;
    }
    if (typeof root.matches === 'function' && root.matches(EDITOR_LIKE_SELECTOR) && hasUsableGeometry(root) && isVisible(root)) {
        return root;
    }
    if (typeof root.querySelectorAll !== 'function') {
        return null;
    }
    for (const candidate of Array.from(root.querySelectorAll(EDITOR_HOST_SELECTOR))) {
        if (candidate.isConnected && hasUsableGeometry(candidate) && isVisible(candidate)) {
            return candidate;
        }
    }
    for (const candidate of Array.from(root.querySelectorAll(EDITOR_LIKE_SELECTOR))) {
        if (candidate.isConnected && hasUsableGeometry(candidate) && isVisible(candidate)) {
            return candidate;
        }
    }
    return null;
}

function resolveInputEndpoint(root: Element): { endpoint: Element; interactionTarget: Element; editorLike?: boolean } | null {
    const doc = root.ownerDocument;
    const active = doc.activeElement;
    if (rootContains(root, active) && isVisibleEditableEndpoint(active)) {
        return {endpoint: active, interactionTarget: active};
    }
    if (isVisibleEditableEndpoint(root)) {
        return {endpoint: root, interactionTarget: root};
    }

    const richEndpoint = firstVisibleEditable(root,
        '[contenteditable]:not([contenteditable="false"]), ' +
        '.ql-editor, .ProseMirror, .ck-editor__editable, .tox-edit-area [contenteditable], ' +
        '.w-e-text-container [contenteditable], .wangEditor-txt, .fr-element, .note-editable',
    );
    if (richEndpoint) {
        return {endpoint: richEndpoint, interactionTarget: richEndpoint};
    }

    const nestedFrames = Array.from((root as ParentNode).querySelectorAll('iframe, frame'))
        .filter((node): node is HTMLIFrameElement | HTMLFrameElement => isFrameElement(node));
    const frames = isFrameElement(root) ? [root, ...nestedFrames] : nestedFrames;
    for (const frame of frames) {
        if (!hasUsableGeometry(frame) || !isVisible(frame)) {
            continue;
        }
        const frameDoc = sameOriginFrameDocument(frame);
        if (!frameDoc) {
            continue;
        }
        const endpoint = firstFrameEditable(frameDoc,
            'body[contenteditable]:not([contenteditable="false"]), ' +
            '[contenteditable]:not([contenteditable="false"])',
        );
        if (endpoint) {
            return {endpoint, interactionTarget: frame};
        }
    }

    const formEndpoint = firstVisibleEditable(root, 'textarea, input');
    if (formEndpoint) {
        return {endpoint: formEndpoint, interactionTarget: formEndpoint};
    }
    const editorLike = closestVisibleEditorLikeContainer(root);
    if (editorLike) {
        return {endpoint: editorLike, interactionTarget: editorLike, editorLike: true};
    }
    return null;
}

function reasonableCompositeControlContainer(element: Element, candidate: Element): boolean {
    const role = candidate.getAttribute('role')?.toLowerCase();
    if (role === 'spinbutton' || role === 'combobox' || role === 'textbox' || role === 'searchbox') {
        return true;
    }
    if (!(candidate instanceof HTMLElement)) {
        return false;
    }
    const className = typeof candidate.className === 'string' ? candidate.className.toLowerCase() : '';
    if (!/(input|control|field|editor|picker|select|number|textarea)/.test(className)) {
        return false;
    }
    const elementRect = firstNonEmptyClientRect(element);
    const candidateRect = firstNonEmptyClientRect(candidate);
    if (!elementRect || !candidateRect) {
        return false;
    }
    if (candidateRect.width < elementRect.width || candidateRect.height < elementRect.height) {
        return false;
    }
    return candidateRect.width <= elementRect.width * 4 && candidateRect.height <= elementRect.height * 4;
}

function resolveActionabilityTarget(element: Element): Element {
    if (!isEditableElement(element)) {
        return element;
    }
    let current = composedParentElement(element);
    let resolved = element;
    let depth = 0;
    while (current && depth < 4) {
        if (reasonableCompositeControlContainer(element, current)) {
            resolved = current;
        }
        current = composedParentElement(current);
        depth++;
    }
    return resolved;
}

function isClickLikePurpose(options?: ActionabilityOptions): boolean {
    return options?.purpose === 'click' || options?.purpose === 'hover';
}

function isHoverPrimingPurpose(options?: ActionabilityOptions): boolean {
    return options?.purpose === 'click' || options?.purpose === 'hover';
}


function isNativelyDisabled(element: Element | null): boolean {
    if (!element || !supportsNativeDisabled(element)) {
        return false;
    }
    if (typeof element.matches === 'function') {
        return element.matches(':disabled');
    }
    return 'disabled' in element && !!(element as HTMLButtonElement).disabled;
}

function isReadOnlyEditable(element: Element | null): boolean {
    return element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement
        ? !!element.readOnly
        : false;
}

function isHardDisabledForPurpose(element: Element, options?: ActionabilityOptions): boolean {
    if (isNativelyDisabled(element)) {
        return true;
    }
    return options?.purpose === 'input' && isReadOnlyEditable(element);
}

function looksLikeCompositeClickContainer(candidate: Element): boolean {
    const tag = candidate.tagName.toLowerCase();
    const role = candidate.getAttribute('role')?.toLowerCase();
    if (
        role === 'button' ||
        role === 'combobox' ||
        role === 'spinbutton' ||
        role === 'textbox' ||
        role === 'searchbox' ||
        role === 'listbox' ||
        tag === 'label'
    ) {
        return true;
    }
    if (!(candidate instanceof HTMLElement)) {
        return false;
    }
    const className = typeof candidate.className === 'string' ? candidate.className.toLowerCase() : '';
    return /(input|control|field|editor|picker|select|number|textarea|autocomplete|dropdown|combobox|date)/.test(className);
}

function isSafeClickCompositeTarget(element: Element, candidate: Element): boolean {
    if (!looksLikeCompositeClickContainer(candidate)) {
        return false;
    }
    if (isNativelyDisabled(candidate)) {
        return false;
    }
    const elementRect = firstNonEmptyClientRect(element);
    const candidateRect = firstNonEmptyClientRect(candidate);
    if (!elementRect || !candidateRect) {
        return false;
    }
    if (candidateRect.width < elementRect.width || candidateRect.height < elementRect.height) {
        return false;
    }
    return candidateRect.width <= elementRect.width * 5 && candidateRect.height <= elementRect.height * 5;
}

function resolveClickActionabilityTarget(element: Element): Element {
    if (!isEditableElement(element) || !isNativelyDisabled(element)) {
        return resolveActionabilityTarget(element);
    }
    let current = composedParentElement(element);
    let depth = 0;
    while (current && depth < 5) {
        if (isSafeClickCompositeTarget(element, current)) {
            return current;
        }
        current = composedParentElement(current);
        depth++;
    }
    return element;
}

function looksLikeClickableTarget(element: Element): boolean {
    const tag = element.tagName.toLowerCase();
    const role = element.getAttribute('role')?.toLowerCase();
    if (tag === 'a' || tag === 'button' || tag === 'summary') {
        return true;
    }
    if (role && ['button', 'link', 'menuitem', 'option', 'tab'].includes(role)) {
        return true;
    }
    if (!(element instanceof HTMLElement)) {
        return false;
    }
    const className = typeof element.className === 'string' ? element.className.toLowerCase() : '';
    if (/(^|\s)(el-link|icon-link|btn|button|link|action|operation)(\s|$|-|_)/.test(className)) {
        return true;
    }
    const cursor = element.ownerDocument.defaultView?.getComputedStyle(element).cursor || '';
    return cursor === 'pointer';
}

function visibleChildClickDelegate(originalTarget: Element, elementLabel: string): ActionabilityDiagnostic | null {
    if (!looksLikeClickableTarget(originalTarget) || !originalTarget.isConnected) {
        return null;
    }
    const doc = originalTarget.ownerDocument;
    const viewport = ownerViewport(originalTarget);
    const candidates = Array.from(originalTarget.querySelectorAll('*')).slice(0, 128);
    for (const candidate of candidates) {
        if (!(candidate instanceof Element) || !candidate.isConnected) {
            continue;
        }
        const view = candidate.ownerDocument.defaultView;
        const style = view?.getComputedStyle(candidate);
        if (!style || style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse' || style.opacity === '0') {
            continue;
        }
        const rects = nonEmptyClientRects(candidate);
        if (rects.length === 0) {
            continue;
        }
        for (const point of sampledPointsForRects(rects, viewport)) {
            const topElement = deepElementFromPoint(doc, point.x, point.y);
            if (isComposedDescendant(originalTarget, topElement) || isComposedDescendant(candidate, topElement)) {
                return actionabilitySuccessDiagnostic(
                    'delegated_clickable',
                    'Element is clickable through visible child delegate',
                    `hidden click target can be activated through visible descendant ${describeElement(candidate)}`,
                    elementLabel,
                    candidate,
                    originalTarget,
                    rectSnapshotFromDOMRect(point.rect),
                    {x: point.x, y: point.y},
                    topElement,
                    'delegate',
                    'visible child delegate inside click target',
                );
            }
        }
    }
    return null;
}

function hoverPrimingCandidate(element: Element): Element | null {
    const selectors = [
        'td, th, [role="cell"], [role="gridcell"], [role="columnheader"]',
        'tr, [role="row"]',
        '.cell',
        '.el-table__row',
        '.el-table__cell',
    ];
    for (const selector of selectors) {
        const closest = typeof element.closest === 'function' ? element.closest(selector) : null;
        if (closest instanceof Element && closest.isConnected && hasUsableGeometry(closest)) {
            return closest;
        }
    }
    let current: Element | null = element;
    let depth = 0;
    while (current && depth < 6) {
        if (current.isConnected && hasUsableGeometry(current)) {
            return current;
        }
        current = composedParentElement(current);
        depth++;
    }
    return null;
}

function hoverPrimingDiagnostic(
    element: Element,
    visibility: VisibilityDiagnostic,
    elementLabel: string,
    rectSnapshot: RectSnapshot | undefined,
): ActionabilityDiagnostic | null {
    if (visibility.code !== 'visibility_hidden') {
        return null;
    }
    const candidate = hoverPrimingCandidate(element);
    if (!candidate) {
        return null;
    }
    const rect = firstNonEmptyClientRect(candidate) || candidate.getBoundingClientRect();
    if (!rect || rect.width <= 0 || rect.height <= 0) {
        return null;
    }
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    return {
        actionable: false,
        kind: 'not_visible',
        summary: 'Element requires hover priming',
        detail: `visibility is hidden before hover; priming target=${describeElement(candidate)}`,
        element: elementLabel,
        culprit: visibility.culprit ? describeElement(visibility.culprit) : undefined,
        localRect: rectSnapshot,
        centerX: x,
        centerY: y,
        retriable: true,
        retryAction: 'hover_priming',
        retryPoint: {x, y},
        retryDelayMs: 90,
        frameChain: [],
    };
}

function isComposedDescendant(target: Element, candidate: Element | null): boolean {
    let current = candidate;
    while (current) {
        if (current === target) {
            return true;
        }
        current = composedParentElement(current);
    }
    return false;
}


function sampledPointOffsets(): Array<[number, number]> {
    return [
        [0.5, 0.5],
        [0.5, 0.25],
        [0.5, 0.75],
        [0.25, 0.5],
        [0.75, 0.5],
    ];
}

function rectArea(rect: DOMRect | null | undefined): number {
    if (!rect || rect.width <= 0 || rect.height <= 0) {
        return 0;
    }
    return rect.width * rect.height;
}

function rectOverlapArea(a: DOMRect | null | undefined, b: DOMRect | null | undefined): number {
    if (!a || !b) {
        return 0;
    }
    const left = Math.max(a.left, b.left);
    const right = Math.min(a.right, b.right);
    const top = Math.max(a.top, b.top);
    const bottom = Math.min(a.bottom, b.bottom);
    if (right <= left || bottom <= top) {
        return 0;
    }
    return (right - left) * (bottom - top);
}

function isSvgDelegateElement(element: Element | null): boolean {
    if (!element) {
        return false;
    }
    switch (element.tagName.toLowerCase()) {
        case 'svg':
        case 'g':
        case 'path':
        case 'use':
        case 'foreignobject':
            return true;
        default:
            return false;
    }
}

function findSvgDelegateRoot(element: Element | null): Element | null {
    let current = element;
    let svgRoot: Element | null = null;
    while (current) {
        if (isSvgDelegateElement(current)) {
            svgRoot = current;
        }
        current = composedParentElement(current);
    }
    return svgRoot;
}

function isLikelyBlockingOverlay(element: Element, targetRect: DOMRect, hitRect: DOMRect): boolean {
    const style = element.ownerDocument?.defaultView?.getComputedStyle(element);
    const areaRatio = rectArea(hitRect) / Math.max(rectArea(targetRect), 1);
    if (hasBlockingOverlaySignal(element)) {
        return true;
    }
    if ((style?.position === 'fixed' || style?.position === 'sticky') && areaRatio > 1.2) {
        return true;
    }
    return areaRatio > 4;
}

function hasBlockingOverlaySignal(element: Element): boolean {
    const signal = `${element.id} ${element.getAttribute('class') || ''} ${element.getAttribute('role') || ''} ${element.getAttribute('aria-label') || ''}`.toLowerCase();
    return /(mask|modal|backdrop|overlay|blocker|loading|spinner|scrim|dialog)/.test(signal);
}

function findInteractiveAncestor(element: Element | null): Element | null {
    let current = element;
    while (current) {
        const tag = current.tagName.toLowerCase();
        const role = current.getAttribute('role')?.toLowerCase();
        if (
            tag === 'a' ||
            tag === 'button' ||
            tag === 'label' ||
            tag === 'summary' ||
            role === 'button' ||
            role === 'link' ||
            role === 'menuitem' ||
            role === 'menuitemcheckbox' ||
            role === 'menuitemradio' ||
            role === 'option' ||
            role === 'tab' ||
            role === 'treeitem' ||
            role === 'gridcell' ||
            current.hasAttribute('onclick') ||
            current.getAttribute('tabindex') === '0'
        ) {
            return current;
        }
        current = composedParentElement(current);
    }
    return null;
}


function nonEmptyClientRects(element: Element): DOMRect[] {
    const rects = Array.from(element.getClientRects()).filter((rect) => rect.width > 0 && rect.height > 0);
    if (rects.length > 0) {
        return rects;
    }
    const bounds = element.getBoundingClientRect();
    return bounds.width > 0 && bounds.height > 0 ? [bounds] : [];
}

function sampledPointsForRects(rects: DOMRect[], viewport: {width: number; height: number}): Array<{x: number; y: number; rect: DOMRect}> {
    const points: Array<{x: number; y: number; rect: DOMRect}> = [];
    const seen = new Set<string>();
    for (const rect of rects) {
        for (const [offsetX, offsetY] of sampledPointOffsets()) {
            const x = rect.left + rect.width * offsetX;
            const y = rect.top + rect.height * offsetY;
            if (x < 0 || x > viewport.width || y < 0 || y > viewport.height) {
                continue;
            }
            const key = `${Math.round(x)}:${Math.round(y)}`;
            if (seen.has(key)) {
                continue;
            }
            seen.add(key);
            points.push({x, y, rect});
        }
    }
    return points;
}

function actionabilitySuccessDiagnostic(
    kind: ActionabilityDiagnostic['kind'],
    summary: string,
    detail: string,
    elementLabel: string,
    actionTarget: Element,
    originalTarget: Element,
    rectSnapshot: RectSnapshot | undefined,
    point: {x: number; y: number},
    topElement: Element | null,
    hitTargetRole: ActionabilityDiagnostic['hitTargetRole'],
    delegateReason?: string,
): ActionabilityDiagnostic {
    const culprit = actionTarget === originalTarget ? undefined : describeElement(actionTarget);
    return {
        actionable: true,
        kind,
        summary,
        detail,
        element: elementLabel,
        culprit,
        localRect: rectSnapshot,
        centerX: point.x,
        centerY: point.y,
        hitTarget: describeElement(topElement),
        hitTargetRole,
        delegateReason,
        frameChain: [],
    };
}

function hasBlockingOverlayAncestor(element: Element | null): boolean {
    let current = element ? composedParentElement(element) : null;
    while (current) {
        if (hasBlockingOverlaySignal(current)) {
            return true;
        }
        current = composedParentElement(current);
    }
    return false;
}

function classifyDelegateHit(targetRect: DOMRect, topElement: Element | null): { culprit: string; hitTarget: string; delegateReason: string } | null {
    if (!topElement) {
        return null;
    }
    const delegateRoot = findSvgDelegateRoot(topElement);
    if (!delegateRoot) {
        return null;
    }
    const hitRect = firstNonEmptyClientRect(delegateRoot) || firstNonEmptyClientRect(topElement);
    if (!hitRect) {
        return null;
    }
    const targetArea = rectArea(targetRect);
    const hitArea = rectArea(hitRect);
    const overlapArea = rectOverlapArea(targetRect, hitRect);
    if (targetArea <= 0 || hitArea <= 0 || overlapArea <= 0) {
        return null;
    }
    const overlapToTarget = overlapArea / targetArea;
    const overlapToHit = overlapArea / hitArea;
    if (isLikelyBlockingOverlay(delegateRoot, targetRect, hitRect) || hasBlockingOverlayAncestor(topElement)) {
        return null;
    }
    const hasLargeDelegateOverlap = overlapToTarget >= 0.55 && overlapToHit >= 0.35;
    const hasSmallInteractiveIcon = overlapToHit >= 0.8 && hitArea <= targetArea * 0.5 && !!findInteractiveAncestor(topElement);
    if (!hasLargeDelegateOverlap && !hasSmallInteractiveIcon) {
        return null;
    }
    return {
        culprit: describeElement(topElement),
        hitTarget: describeElement(delegateRoot),
        delegateReason: hasSmallInteractiveIcon
            ? 'hit test resolves to svg icon inside interactive delegate'
            : overlapToTarget >= 0.8
            ? 'hit test resolves to overlapping svg delegate layer'
            : 'hit test resolves to partially overlapping svg delegate layer',
    };
}

export function describeElement(el: Element | null): string {
    if (!el) {
        return "null";
    }
    const tag = el.tagName.toLowerCase();
    const id = el.id ? `#${el.id}` : "";
    const classNames = typeof (el as HTMLElement).className === 'string'
        ? (el as HTMLElement).className.trim().split(/\s+/).filter(Boolean).slice(0, 3).map((name) => `.${name}`).join('')
        : "";
    const role = el.getAttribute('role');
    const type = el.getAttribute('type');
    const ariaLabel = el.getAttribute('aria-label');
    const title = el.getAttribute('title');
    const text = (el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 30);
    const extras = [
        role ? `role=${JSON.stringify(role)}` : "",
        type ? `type=${JSON.stringify(type)}` : "",
        ariaLabel ? `aria-label=${JSON.stringify(ariaLabel)}` : "",
        title ? `title=${JSON.stringify(title)}` : "",
        text ? `text=${JSON.stringify(text)}` : "",
    ].filter(Boolean);
    return extras.length > 0
        ? `${tag}${id}${classNames} [${extras.join(', ')}]`
        : `${tag}${id}${classNames}`;
}

export const cdpLocator: CdpLocator = {
    isVisible: (el: Element | null) => isVisible(el),
    isStable: (el: Element, options?: ActionabilityOptions) => isStable(el, options),
    isEnabled: (el: Element) => isEnabled(el),

    checkActionability: async (el: Element | null, options?: ActionabilityOptions) => {
        if (!el) {
            return { actionable: false, kind: 'not_found', summary: 'Element not found', detail: 'element reference is null', element: 'null', frameChain: [] };
        }
        if (!el.isConnected) {
            return { actionable: false, kind: 'detached', summary: 'Element is detached', detail: 'element is detached from DOM', element: describeElement(el), frameChain: [] };
        }

        const clickLike = isClickLikePurpose(options);
        const inputTarget = options?.purpose === 'input' ? resolveInputEndpoint(el) : null;
        const elementLabel = describeElement(el);
        if (options?.purpose === 'input' && !inputTarget) {
            const rect = firstNonEmptyClientRect(el) || el.getBoundingClientRect();
            return {
                actionable: false,
                kind: 'not_visible',
                summary: 'Element has no editable input target',
                detail: 'no visible input, textarea, contenteditable, or same-origin rich text endpoint was found',
                element: elementLabel,
                localRect: rectSnapshotFromDOMRect(rect),
                frameChain: [],
            };
        }
        const actionTarget = inputTarget?.interactionTarget || (clickLike ? resolveClickActionabilityTarget(el) : resolveActionabilityTarget(el));
        const editableEndpoint = inputTarget?.endpoint || el;
        const actionTargetLabel = actionTarget === el ? '' : describeElement(actionTarget);
        let targetVisibility = inputTarget && inputTarget.interactionTarget !== editableEndpoint
            ? diagnoseVisibility(inputTarget.interactionTarget)
            : diagnoseVisibility(editableEndpoint);
        let actionVisibility = actionTarget === el ? targetVisibility : diagnoseVisibility(actionTarget);
        let rectSnapshot = rectSnapshotFromDOMRect(
            (actionTarget === el ? targetVisibility : actionVisibility).rect
            || firstNonEmptyClientRect(actionTarget)
            || actionTarget.getBoundingClientRect(),
        );

        if (!targetVisibility.visible) {
            if (clickLike && !inputTarget && targetVisibility.code === 'visibility_hidden') {
                const delegate = visibleChildClickDelegate(el, elementLabel);
                if (delegate) {
                    return delegate;
                }
                if (isHoverPrimingPurpose(options)) {
                    const priming = hoverPrimingDiagnostic(el, targetVisibility, elementLabel, rectSnapshot);
                    if (priming) {
                        return priming;
                    }
                }
            }
            const culpritLabel = targetVisibility.culprit && targetVisibility.culprit !== editableEndpoint ? describeElement(targetVisibility.culprit) : "";
            const detail = culpritLabel
                ? `${culpritLabel} causes ${targetVisibility.detail}`
                : targetVisibility.detail;
            return {
                actionable: false,
                kind: 'not_visible',
                summary: inputTarget ? 'Editable input target is not visible' : 'Element is not visible',
                detail,
                element: elementLabel,
                culprit: culpritLabel || undefined,
                localRect: rectSnapshot,
                frameChain: [],
            };
        }

        if (!actionVisibility.visible) {
            if (clickLike && !inputTarget && actionVisibility.code === 'visibility_hidden') {
                const delegate = visibleChildClickDelegate(actionTarget, elementLabel);
                if (delegate) {
                    return delegate;
                }
                if (isHoverPrimingPurpose(options)) {
                    const priming = hoverPrimingDiagnostic(actionTarget, actionVisibility, elementLabel, rectSnapshot);
                    if (priming) {
                        return priming;
                    }
                }
            }
            const culpritLabel = actionVisibility.culprit && actionVisibility.culprit !== actionTarget ? describeElement(actionVisibility.culprit) : actionTargetLabel;
            const detail = culpritLabel
                ? `${culpritLabel} causes ${actionVisibility.detail}`
                : actionVisibility.detail;
            return {
                actionable: false,
                kind: 'not_visible',
                summary: 'Element action target is not visible',
                detail,
                element: elementLabel,
                culprit: culpritLabel || undefined,
                localRect: rectSnapshot,
                frameChain: [],
            };
        }

        const targetDisabled = inputTarget?.editorLike ? false : isHardDisabledForPurpose(editableEndpoint, options);
        const actionTargetDisabled = actionTarget !== el && isHardDisabledForPurpose(actionTarget, options);
        if ((!clickLike && targetDisabled) || actionTargetDisabled || (clickLike && targetDisabled && actionTarget === el)) {
            const detail = clickLike && targetDisabled && actionTarget === el
                ? 'disabled control and no clickable composite ancestor was found'
                : actionTargetDisabled
                ? `action target ${actionTargetLabel} is natively disabled or readonly`
                : 'native disabled control or readonly editable control';
            return { actionable: false, kind: 'disabled', summary: 'Element is disabled', detail, element: elementLabel, localRect: rectSnapshot, frameChain: [] };
        }

        let stability: ActionabilityDiagnostic['stability'];
        if (!options?.skipStability) {
            const result = await diagnoseStability(actionTarget, options);
            stability = {
                kind: result.kind,
                elapsedMs: Math.round(result.elapsedMs),
                maxDelta: {
                    x: result.maxDelta.x,
                    y: result.maxDelta.y,
                    width: result.maxDelta.width,
                    height: result.maxDelta.height,
                },
                samples: result.samples.map((rect) => ({x: rect.left, y: rect.top, width: rect.width, height: rect.height})),
            };
            if (!result.stable) {
                const targetDetail = actionTarget === el ? 'element' : `action target ${actionTargetLabel}`;
                if (result.kind === 'detached') {
                    return {
                        actionable: false,
                        kind: 'detached',
                        summary: 'Element is detached',
                        detail: `${targetDetail} was detached while waiting for stable geometry`,
                        element: elementLabel,
                        culprit: actionTarget === el ? undefined : actionTargetLabel,
                        localRect: rectSnapshotFromDOMRect(result.rect) || rectSnapshot,
                        stability,
                        frameChain: [],
                    };
                }
                if (result.kind === 'unavailable') {
                    return {
                        actionable: false,
                        kind: 'unavailable',
                        summary: 'Element geometry is unavailable',
                        detail: `${targetDetail} has no reachable owner window`,
                        element: elementLabel,
                        culprit: actionTarget === el ? undefined : actionTargetLabel,
                        localRect: rectSnapshot,
                        stability,
                        frameChain: [],
                    };
                }
                const delta = result.maxDelta;
                return {
                    actionable: false,
                    kind: 'unstable',
                    summary: 'Element is unstable',
                    detail: `${targetDetail} did not settle within ${Math.round(result.elapsedMs)}ms; max delta x=${delta.x.toFixed(2)}, y=${delta.y.toFixed(2)}, width=${delta.width.toFixed(2)}, height=${delta.height.toFixed(2)}`,
                    element: elementLabel,
                    culprit: actionTarget === el ? undefined : actionTargetLabel,
                    localRect: rectSnapshotFromDOMRect(result.rect) || rectSnapshot,
                    stability,
                    frameChain: [],
                };
            }
        }

        if (!el.isConnected || !actionTarget.isConnected || !editableEndpoint.isConnected) {
            return {
                actionable: false,
                kind: 'detached',
                summary: 'Element is detached',
                detail: 'element or its action target was detached after geometry stabilization',
                element: elementLabel,
                culprit: actionTarget === el ? undefined : actionTargetLabel,
                localRect: rectSnapshot,
                stability,
                frameChain: [],
            };
        }

        targetVisibility = inputTarget && inputTarget.interactionTarget !== editableEndpoint
            ? diagnoseVisibility(inputTarget.interactionTarget)
            : diagnoseVisibility(editableEndpoint);
        actionVisibility = actionTarget === el ? targetVisibility : diagnoseVisibility(actionTarget);
        if (!targetVisibility.visible || !actionVisibility.visible) {
            const visibility = !targetVisibility.visible ? targetVisibility : actionVisibility;
            const culprit = visibility.culprit && visibility.culprit !== actionTarget
                ? describeElement(visibility.culprit)
                : actionTarget === el ? '' : actionTargetLabel;
            return {
                actionable: false,
                kind: 'not_visible',
                summary: 'Element became invisible',
                detail: culprit ? `${culprit} causes ${visibility.detail}` : visibility.detail,
                element: elementLabel,
                culprit: culprit || undefined,
                localRect: rectSnapshotFromDOMRect(visibility.rect) || rectSnapshot,
                stability,
                frameChain: [],
            };
        }

        const geometrySource = inputTarget ? actionTarget : (clickLike && actionTarget !== el ? actionTarget : el);
        const geometryVisibility = geometrySource === el ? targetVisibility : actionVisibility;
        const geometryRect = geometryVisibility.rect || firstNonEmptyClientRect(geometrySource) || geometrySource.getBoundingClientRect();
        rectSnapshot = rectSnapshotFromDOMRect(geometryRect);

        const hasGeometry = !!geometryRect && geometryRect.width > 0 && geometryRect.height > 0;
        if (!hasGeometry) {
            return {
                actionable: false,
                kind: 'not_visible',
                summary: 'Element has no clickable geometry',
                detail: 'element has no non-empty client rects',
                element: elementLabel,
                localRect: rectSnapshot,
                frameChain: [],
            };
        }

        const rect = geometryRect;
        const viewport = ownerViewport(el);
        const doc = el.ownerDocument;
        let firstInViewportPoint: { x: number; y: number } | null = null;
        let firstObstruction: { x: number; y: number; topElement: Element | null } | null = null;
        const sampleRects = [rect];
        const samplePoints = sampledPointsForRects(sampleRects, viewport);

        for (const {x, y} of samplePoints) {
            if (!firstInViewportPoint) {
                firstInViewportPoint = {x, y};
            }
            const topElement = deepElementFromPoint(doc, x, y);
            if (actionTarget !== el && targetDisabled && isComposedDescendant(editableEndpoint, topElement)) {
                if (!firstObstruction) {
                    firstObstruction = {x, y, topElement};
                }
                continue;
            }
            if (
                isComposedDescendant(editableEndpoint, topElement) ||
                isComposedDescendant(el, topElement) ||
                (actionTarget !== el && isComposedDescendant(actionTarget, topElement))
            ) {
                return {
                    actionable: true,
                    kind: 'ok',
                    summary: 'Element is actionable',
                    detail: actionTarget !== el && targetDisabled
                        ? `disabled inner control retargeted to clickable composite ancestor ${actionTargetLabel}`
                        : '',
                    element: elementLabel,
                    culprit: actionTarget === el ? undefined : actionTargetLabel,
                    localRect: rectSnapshot,
                    stability,
                    centerX: x,
                    centerY: y,
                    hitTarget: describeElement(topElement),
                    hitTargetRole: topElement === el || topElement === actionTarget ? 'self' : 'descendant',
                    frameChain: [],
                };
            }

            const delegate = classifyDelegateHit(rect, topElement);
            if (delegate) {
                return {
                    actionable: true,
                    kind: 'delegated_clickable',
                    summary: 'Element is clickable through delegate layer',
                    detail: `hit test at (${x.toFixed(0)}, ${y.toFixed(0)}) resolves to ${delegate.culprit}`,
                    element: elementLabel,
                    culprit: delegate.culprit,
                    hitTarget: delegate.hitTarget,
                    hitTargetRole: 'delegate',
                    delegateReason: delegate.delegateReason,
                    localRect: rectSnapshot,
                    stability,
                    centerX: x,
                    centerY: y,
                    frameChain: [],
                };
            }
            if (!firstObstruction) {
                firstObstruction = {x, y, topElement};
            }
        }

        if (!firstInViewportPoint) {
            const centerX = rect.left + rect.width / 2;
            const centerY = rect.top + rect.height / 2;
            if (options?.allowOutsideViewport) {
                return {
                    actionable: true,
                    kind: 'ok',
                    summary: 'Element is outside viewport',
                    detail: `${options.purpose || 'action'} target is outside viewport and will be scrolled before dispatch`,
                    element: elementLabel,
                    culprit: actionTarget === el ? undefined : actionTargetLabel,
                    localRect: rectSnapshot,
                    stability,
                    centerX,
                    centerY,
                    frameChain: [],
                };
            }
            return {
                actionable: false,
                kind: 'outside_viewport',
                summary: 'Element center is outside viewport',
                detail: `sampled hit points are outside viewport ${viewport.width}x${viewport.height}`,
                element: elementLabel,
                localRect: rectSnapshot,
                stability,
                centerX,
                centerY,
                frameChain: [],
            };
        }


        const obstructionX = firstObstruction?.x ?? firstInViewportPoint.x;
        const obstructionY = firstObstruction?.y ?? firstInViewportPoint.y;
        const topDetail = describeElement(firstObstruction?.topElement ?? null);
        return {
            actionable: false,
            kind: 'obscured',
            summary: 'Element is obscured',
            detail: `hit test at (${obstructionX.toFixed(0)}, ${obstructionY.toFixed(0)}) resolves to ${topDetail}`,
            element: elementLabel,
            culprit: topDetail,
            hitTarget: topDetail,
            hitTargetRole: 'obstructor',
            localRect: rectSnapshot,
            stability,
            centerX: obstructionX,
            centerY: obstructionY,
            frameChain: [],
        };
    }
};

export function createLocator(): CdpLocator {
    return cdpLocator;
}

export const InjectLocator = createLocator;
