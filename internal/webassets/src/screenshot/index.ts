import {composedParentElement} from "../utils/dom";
import {projectPointToTopWindow} from "../query/runtime";
import type {FrameElement} from "../utils/frame_element";
import {listInternalHTMLElements} from "../utils/internal_ui";

export interface ScreenshotRect {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface ScreenshotTile {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface ScreenshotTileResult {
    source: ScreenshotRect;
    dest: ScreenshotRect;
    freshFrame?: boolean;
}

export interface ScreenshotPlan {
    token: string;
    width: number;
    height: number;
    tiles: ScreenshotTile[];
}

type Axis = 'x' | 'y';

interface StyleRestore {
    element: HTMLElement;
    value: string;
    priority: string;
}

interface OverlayVisibilityRestore {
    element: HTMLElement;
    value: string;
    priority: string;
}

interface PageScreenshotSession {
    token: string;
    originalX: number;
    originalY: number;
    width: number;
    height: number;
    canX: boolean;
    canY: boolean;
    maxScrollX: number;
    maxScrollY: number;
    restores: StyleRestore[];
    overlayRestores: OverlayVisibilityRestore[];
}

interface ScrollTarget {
    kind: 'self' | 'element' | 'window';
    element?: HTMLElement;
    canX: boolean;
    canY: boolean;
    originalX: number;
    originalY: number;
    maxScrollX: number;
    maxScrollY: number;
    restores: StyleRestore[];
}

interface ClipTarget {
    kind: 'element' | 'viewport';
    element?: HTMLElement;
    clipsX: boolean;
    clipsY: boolean;
}

interface ElementScreenshotSession {
    token: string;
    target: Element;
    originX: number;
    originY: number;
    width: number;
    height: number;
    tiles: ScreenshotTile[];
    scrollTargets: ScrollTarget[];
    clipTargets: ClipTarget[];
    overlayRestores: OverlayVisibilityRestore[];
    selfScrollX: boolean;
    selfScrollY: boolean;
}

const RECT_EPSILON = 0.001;
const SCROLL_EPSILON = 0.5;
const MAX_TILES = 400;

const pageSessions = new Map<string, PageScreenshotSession>();
const elementSessions = new Map<string, ElementScreenshotSession>();

function makeToken(prefix: string): string {
    return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
}

function emptyRect(): ScreenshotRect {
    return {x: 0, y: 0, width: 0, height: 0};
}

function isFinitePositive(value: number): boolean {
    return Number.isFinite(value) && value > RECT_EPSILON;
}

function clamp(value: number, min: number, max: number): number {
    if (max < min) {
        return min;
    }
    return Math.max(min, Math.min(max, value));
}

function viewportSize(currentWindow: Window = window): {width: number; height: number} {
    const visual = currentWindow.visualViewport;
    const width = visual?.width || currentWindow.innerWidth || currentWindow.document.documentElement?.clientWidth || 1;
    const height = visual?.height || currentWindow.innerHeight || currentWindow.document.documentElement?.clientHeight || 1;
    return {
        width: Math.max(1, width),
        height: Math.max(1, height),
    };
}

function scrollOffset(currentWindow: Window = window): {x: number; y: number} {
    return {
        x: currentWindow.scrollX || currentWindow.pageXOffset || 0,
        y: currentWindow.scrollY || currentWindow.pageYOffset || 0,
    };
}

function visualPageOffset(currentWindow: Window = window): {x: number; y: number} {
    const visual = currentWindow.visualViewport;
    const scroll = scrollOffset(currentWindow);
    if (!visual) {
        return scroll;
    }
    return {
        x: scroll.x + (Number.isFinite(visual.offsetLeft) ? visual.offsetLeft : 0),
        y: scroll.y + (Number.isFinite(visual.offsetTop) ? visual.offsetTop : 0),
    };
}

function settleScreenshotScroll(): Promise<void> {
    return new Promise((resolve) => {
        window.setTimeout(resolve, 50);
    });
}

function saveAndDisableSmoothScroll(element: HTMLElement): StyleRestore {
    const value = element.style.getPropertyValue('scroll-behavior');
    const priority = element.style.getPropertyPriority('scroll-behavior');
    element.style.setProperty('scroll-behavior', 'auto', 'important');
    return {element, value, priority};
}

function restoreStyle(restore: StyleRestore): void {
    if (restore.value) {
        restore.element.style.setProperty('scroll-behavior', restore.value, restore.priority);
        return;
    }
    restore.element.style.removeProperty('scroll-behavior');
}

function restoreStyles(restores: StyleRestore[]): void {
    for (const restore of restores) {
        restoreStyle(restore);
    }
}

export function suspendInjectedOverlays(): OverlayVisibilityRestore[] {
    const restores: OverlayVisibilityRestore[] = [];
    for (const element of listInternalHTMLElements()) {
        restores.push({
            element,
            value: element.style.getPropertyValue('visibility'),
            priority: element.style.getPropertyPriority('visibility'),
        });
        element.style.setProperty('visibility', 'hidden', 'important');
    }
    return restores;
}

export function restoreInjectedOverlays(restores: OverlayVisibilityRestore[]): void {
    for (let i = restores.length - 1; i >= 0; i--) {
        const restore = restores[i];
        if (restore.value) {
            restore.element.style.setProperty('visibility', restore.value, restore.priority);
            continue;
        }
        restore.element.style.removeProperty('visibility');
    }
}

function overflowValue(element: Element, axis: Axis): string {
    const style = element.ownerDocument.defaultView?.getComputedStyle(element);
    if (!style) {
        return 'visible';
    }
    return (axis === 'x' ? style.overflowX : style.overflowY || style.overflow).toLowerCase();
}

function isOverflowDisabled(value: string): boolean {
    return value === 'hidden' || value === 'clip';
}

function isScrollableOverflow(value: string): boolean {
    return value === 'auto' || value === 'scroll' || value === 'overlay';
}

function isClippingOverflow(value: string): boolean {
    return value !== '' && value !== 'visible';
}

function documentScrollSize(axis: Axis, root: HTMLElement, body: HTMLElement | null, viewportLength: number): number {
    const values = axis === 'x'
        ? [root.scrollWidth, root.offsetWidth, root.clientWidth, body?.scrollWidth || 0, body?.offsetWidth || 0, body?.clientWidth || 0, viewportLength]
        : [root.scrollHeight, root.offsetHeight, root.clientHeight, body?.scrollHeight || 0, body?.offsetHeight || 0, body?.clientHeight || 0, viewportLength];
    return Math.max(1, ...values.filter((value) => Number.isFinite(value)));
}

function documentAxisCanScroll(axis: Axis, root: HTMLElement, body: HTMLElement | null, viewportLength: number): {can: boolean; size: number; maxScroll: number} {
    const rootOverflow = overflowValue(root, axis);
    const bodyOverflow = body ? overflowValue(body, axis) : 'visible';
    const size = documentScrollSize(axis, root, body, viewportLength);
    const maxScroll = Math.max(0, size - viewportLength);
    if (maxScroll <= SCROLL_EPSILON || isOverflowDisabled(rootOverflow) || isOverflowDisabled(bodyOverflow)) {
        return {can: false, size, maxScroll: 0};
    }
    return {can: true, size, maxScroll};
}

function tilePositions(total: number, aperture: number): number[] {
    const length = Math.max(1, total);
    const step = Math.max(1, Math.min(aperture, length));
    if (length <= step + RECT_EPSILON) {
        return [0];
    }
    const positions: number[] = [];
    for (let value = 0; value < length - RECT_EPSILON; value += step) {
        positions.push(Math.min(value, Math.max(0, length - step)));
        if (positions.length > MAX_TILES) {
            throw new Error('screenshot tile count is too large');
        }
    }
    positions.push(Math.max(0, length - step));
    return Array.from(new Set(positions.map((value) => Math.round(value * 1000) / 1000)));
}

function makeTiles(width: number, height: number, apertureWidth: number, apertureHeight: number): ScreenshotTile[] {
    const xs = tilePositions(width, apertureWidth);
    const ys = tilePositions(height, apertureHeight);
    const tiles: ScreenshotTile[] = [];
    for (const y of ys) {
        for (const x of xs) {
            tiles.push({
                x,
                y,
                width: Math.min(apertureWidth, width - x),
                height: Math.min(apertureHeight, height - y),
            });
        }
    }
    if (tiles.length > MAX_TILES) {
        throw new Error('screenshot tile count is too large');
    }
    return tiles;
}

function intersectRect(a: ScreenshotRect, b: ScreenshotRect): ScreenshotRect | null {
    const left = Math.max(a.x, b.x);
    const top = Math.max(a.y, b.y);
    const right = Math.min(a.x + a.width, b.x + b.width);
    const bottom = Math.min(a.y + a.height, b.y + b.height);
    if (right <= left + RECT_EPSILON || bottom <= top + RECT_EPSILON) {
        return null;
    }
    return {x: left, y: top, width: right - left, height: bottom - top};
}

function intersectRectAxes(rect: ScreenshotRect, clip: ScreenshotRect, useX: boolean, useY: boolean): ScreenshotRect | null {
    let left = rect.x;
    let right = rect.x + rect.width;
    let top = rect.y;
    let bottom = rect.y + rect.height;
    if (useX) {
        left = Math.max(left, clip.x);
        right = Math.min(right, clip.x + clip.width);
    }
    if (useY) {
        top = Math.max(top, clip.y);
        bottom = Math.min(bottom, clip.y + clip.height);
    }
    if (right <= left + RECT_EPSILON || bottom <= top + RECT_EPSILON) {
        return null;
    }
    return {x: left, y: top, width: right - left, height: bottom - top};
}

function elementClientRect(element: HTMLElement): ScreenshotRect {
    const rect = element.getBoundingClientRect();
    return {
        x: rect.left + element.clientLeft,
        y: rect.top + element.clientTop,
        width: Math.max(0, element.clientWidth),
        height: Math.max(0, element.clientHeight),
    };
}

function elementScrollInfo(element: HTMLElement): {canX: boolean; canY: boolean; maxScrollX: number; maxScrollY: number} {
    const overflowX = overflowValue(element, 'x');
    const overflowY = overflowValue(element, 'y');
    const maxScrollX = Math.max(0, element.scrollWidth - element.clientWidth);
    const maxScrollY = Math.max(0, element.scrollHeight - element.clientHeight);
    return {
        canX: isScrollableOverflow(overflowX) && maxScrollX > SCROLL_EPSILON,
        canY: isScrollableOverflow(overflowY) && maxScrollY > SCROLL_EPSILON,
        maxScrollX,
        maxScrollY,
    };
}

function topWindowFor(currentWindow: Window): Window | null {
    let iter: Window | null = currentWindow;
    try {
        while (iter && iter.parent && iter !== iter.parent) {
            iter = iter.parent;
        }
        return iter;
    } catch {
        return null;
    }
}

function projectRectToTopWindow(currentWindow: Window | null, rect: ScreenshotRect): ScreenshotRect | null {
    if (!currentWindow) return null;
    const point = projectPointToTopWindow(currentWindow, rect);
    return point ? {...rect, ...point} : null;
}

function currentTopViewportRect(sourceWindow: Window): ScreenshotRect | null {
    const topWindow = topWindowFor(sourceWindow);
    if (!topWindow) {
        return null;
    }
    const size = viewportSize(topWindow);
    return {x: 0, y: 0, width: size.width, height: size.height};
}

function clipScreenshotTile(tile: ScreenshotTileResult, viewport: ScreenshotRect | null): ScreenshotTileResult {
    const {source, dest, freshFrame} = tile;
    const clipped = viewport && intersectRect(source, viewport);
    if (!clipped) return {source: emptyRect(), dest: emptyRect(), freshFrame};
    const scaleX = source.width > RECT_EPSILON ? dest.width / source.width : 1;
    const scaleY = source.height > RECT_EPSILON ? dest.height / source.height : 1;
    return {
        source: clipped,
        dest: {
            x: dest.x + (clipped.x - source.x) * scaleX,
            y: dest.y + (clipped.y - source.y) * scaleY,
            width: clipped.width * scaleX,
            height: clipped.height * scaleY,
        },
        freshFrame,
    };
}

// Each authenticated parent hop projects and clips in its own document.
export function projectChildScreenshotTile(frame: FrameElement, tile: ScreenshotTileResult): ScreenshotTileResult {
    const frameRect = frame.getBoundingClientRect();
    const offsetX = frameRect.left + frame.clientLeft;
    const offsetY = frameRect.top + frame.clientTop;
    const source = {...tile.source, x: tile.source.x + offsetX, y: tile.source.y + offsetY};
    const viewport = viewportSize(window);
    return clipScreenshotTile({...tile, source}, {x: 0, y: 0, width: viewport.width, height: viewport.height});
}

export type ScreenshotTileProjector = (tile: ScreenshotTileResult) => Promise<ScreenshotTileResult>;

export async function beginPageScreenshot(): Promise<ScreenshotPlan> {
    const root = (document.scrollingElement || document.documentElement) as HTMLElement;
    const body = document.body;
    const viewport = viewportSize(window);
    const xInfo = documentAxisCanScroll('x', root, body, viewport.width);
    const yInfo = documentAxisCanScroll('y', root, body, viewport.height);
    const width = xInfo.can ? xInfo.size : Math.min(xInfo.size, viewport.width);
    const height = yInfo.can ? yInfo.size : Math.min(yInfo.size, viewport.height);
    if (!isFinitePositive(width) || !isFinitePositive(height)) {
        throw new Error('invalid page screenshot size');
    }

    const token = makeToken('page_shot');
    const original = scrollOffset(window);
    const restores = [saveAndDisableSmoothScroll(root)];
    if (body && body !== root) {
        restores.push(saveAndDisableSmoothScroll(body));
    }
    const tiles = makeTiles(width, height, xInfo.can ? viewport.width : width, yInfo.can ? viewport.height : height);
    const overlayRestores = suspendInjectedOverlays();
    const session: PageScreenshotSession = {
        token,
        originalX: original.x,
        originalY: original.y,
        width,
        height,
        canX: xInfo.can,
        canY: yInfo.can,
        maxScrollX: xInfo.maxScroll,
        maxScrollY: yInfo.maxScroll,
        restores,
        overlayRestores,
    };
    pageSessions.set(token, session);
    return {token, width, height, tiles};
}

export async function applyPageScreenshotTile(token: string, tile: ScreenshotTile): Promise<ScreenshotTileResult> {
    const session = pageSessions.get(token);
    if (!session) {
        throw new Error('page screenshot session not found');
    }
    const before = visualPageOffset(window);
    const desiredX = session.canX ? clamp(tile.x, 0, session.maxScrollX) : session.originalX;
    const desiredY = session.canY ? clamp(tile.y, 0, session.maxScrollY) : session.originalY;
    window.scrollTo(desiredX, desiredY);
    await settleScreenshotScroll();

    const viewport = viewportSize(window);
    const actual = visualPageOffset(window);
    const freshFrame = Math.abs(actual.x - before.x) > SCROLL_EPSILON || Math.abs(actual.y - before.y) > SCROLL_EPSILON;
    const tileRight = tile.x + tile.width;
    const tileBottom = tile.y + tile.height;
    const visibleLeft = session.canX ? Math.max(tile.x, actual.x) : 0;
    const visibleTop = session.canY ? Math.max(tile.y, actual.y) : 0;
    let visibleRight = session.canX ? Math.min(tileRight, actual.x + viewport.width, session.width) : Math.min(tileRight, viewport.width, session.width);
    let visibleBottom = session.canY ? Math.min(tileBottom, actual.y + viewport.height, session.height) : Math.min(tileBottom, viewport.height, session.height);
    if (session.canX && tileRight >= session.width - SCROLL_EPSILON && actual.x + viewport.width >= session.width - SCROLL_EPSILON) {
        visibleRight = Math.min(tileRight, session.width);
    }
    if (session.canY && tileBottom >= session.height - SCROLL_EPSILON && actual.y + viewport.height >= session.height - SCROLL_EPSILON) {
        visibleBottom = Math.min(tileBottom, session.height);
    }
    if (visibleRight <= visibleLeft + RECT_EPSILON || visibleBottom <= visibleTop + RECT_EPSILON) {
        return {source: emptyRect(), dest: emptyRect(), freshFrame};
    }
    return {
        source: {
            x: session.canX ? visibleLeft - actual.x : 0,
            y: session.canY ? visibleTop - actual.y : 0,
            width: visibleRight - visibleLeft,
            height: visibleBottom - visibleTop,
        },
        dest: {
            x: visibleLeft,
            y: visibleTop,
            width: visibleRight - visibleLeft,
            height: visibleBottom - visibleTop,
        },
        freshFrame,
    };
}

export async function finishPageScreenshot(token: string): Promise<void> {
    const session = pageSessions.get(token);
    if (!session) {
        return;
    }
    pageSessions.delete(token);
    window.scrollTo(session.originalX, session.originalY);
    restoreStyles(session.restores);
    restoreInjectedOverlays(session.overlayRestores);
    await settleScreenshotScroll();
}

function isDocumentScreenshotRoot(target: Element | null): boolean {
    return !!target && target.isConnected && (target === document.body || target === document.documentElement);
}

export async function beginFrameDocumentScreenshot(target: Element | null): Promise<ScreenshotPlan> {
    if (!isDocumentScreenshotRoot(target)) {
        throw new Error('frame document screenshot target must be body or html');
    }
    return beginPageScreenshot();
}

export async function applyFrameDocumentScreenshotTile(target: Element | null, token: string, tile: ScreenshotTile, project: ScreenshotTileProjector): Promise<ScreenshotTileResult> {
    if (!isDocumentScreenshotRoot(target)) {
        throw new Error('frame document screenshot target must be body or html');
    }
    const result = await applyPageScreenshotTile(token, tile);
    if (result.source.width <= RECT_EPSILON || result.source.height <= RECT_EPSILON || result.dest.width <= RECT_EPSILON || result.dest.height <= RECT_EPSILON) {
        return result;
    }
    const projected = projectRectToTopWindow(window, result.source);
    if (!projected) {
        return project(result);
    }
    return clipScreenshotTile({...result, source: projected}, currentTopViewportRect(window));
}

export async function finishFrameDocumentScreenshot(token: string): Promise<void> {
    return finishPageScreenshot(token);
}

function collectElementScreenshotState(target: Element): {
    scrollTargets: ScrollTarget[];
    clipTargets: ClipTarget[];
    bounds: ScreenshotRect;
    apertureWidth: number;
    apertureHeight: number;
    selfScrollX: boolean;
    selfScrollY: boolean;
} {
    const targetRect = target.getBoundingClientRect();
    let bounds: ScreenshotRect = {x: 0, y: 0, width: targetRect.width, height: targetRect.height};
    const scrollTargets: ScrollTarget[] = [];
    const clipTargets: ClipTarget[] = [];
    const viewport = viewportSize(window);
    let apertureWidth = Math.max(1, Math.min(bounds.width, viewport.width));
    let apertureHeight = Math.max(1, Math.min(bounds.height, viewport.height));
    const targetElement = target instanceof HTMLElement ? target : null;
    const targetScroll = targetElement ? elementScrollInfo(targetElement) : null;
    const selfScrollX = !!targetScroll?.canX;
    const selfScrollY = !!targetScroll?.canY;

    if (targetElement && targetScroll && (selfScrollX || selfScrollY)) {
        const clientWidth = Math.max(1, targetElement.clientWidth || targetRect.width);
        const clientHeight = Math.max(1, targetElement.clientHeight || targetRect.height);
        bounds = {
            x: 0,
            y: 0,
            width: selfScrollX ? Math.max(clientWidth, targetElement.scrollWidth) : clientWidth,
            height: selfScrollY ? Math.max(clientHeight, targetElement.scrollHeight) : clientHeight,
        };
        apertureWidth = Math.max(1, Math.min(bounds.width, clientWidth, viewport.width));
        apertureHeight = Math.max(1, Math.min(bounds.height, clientHeight, viewport.height));
        scrollTargets.push({
            kind: 'self',
            element: targetElement,
            canX: selfScrollX,
            canY: selfScrollY,
            originalX: targetElement.scrollLeft,
            originalY: targetElement.scrollTop,
            maxScrollX: targetScroll.maxScrollX,
            maxScrollY: targetScroll.maxScrollY,
            restores: [saveAndDisableSmoothScroll(targetElement)],
        });
    }

    for (let current = composedParentElement(target); current; current = composedParentElement(current)) {
        if (!(current instanceof HTMLElement)) {
            continue;
        }
        const currentScroll = elementScrollInfo(current);
        const overflowX = overflowValue(current, 'x');
        const overflowY = overflowValue(current, 'y');
        const clipsX = isClippingOverflow(overflowX);
        const clipsY = isClippingOverflow(overflowY);
        const canX = currentScroll.canX;
        const canY = currentScroll.canY;

        if (clipsX || clipsY) {
            clipTargets.push({
                kind: 'element',
                element: current,
                clipsX,
                clipsY,
            });
            if (clipsX) {
                apertureWidth = Math.max(1, Math.min(apertureWidth, current.clientWidth || apertureWidth));
            }
            if (clipsY) {
                apertureHeight = Math.max(1, Math.min(apertureHeight, current.clientHeight || apertureHeight));
            }
        }
        if (canX || canY) {
            scrollTargets.push({
                kind: 'element',
                element: current,
                canX,
                canY,
                originalX: current.scrollLeft,
                originalY: current.scrollTop,
                maxScrollX: currentScroll.maxScrollX,
                maxScrollY: currentScroll.maxScrollY,
                restores: [saveAndDisableSmoothScroll(current)],
            });
        }

        const clipsBoundsX = clipsX && !canX && !selfScrollX;
        const clipsBoundsY = clipsY && !canY && !selfScrollY;
        if (clipsBoundsX || clipsBoundsY) {
            const clip = elementClientRect(current);
            const localClip = {
                x: clip.x - targetRect.left,
                y: clip.y - targetRect.top,
                width: clip.width,
                height: clip.height,
            };
            const next = intersectRectAxes(bounds, localClip, clipsBoundsX, clipsBoundsY);
            if (!next) {
                bounds = emptyRect();
                break;
            }
            bounds = next;
        }
    }

    const root = (document.scrollingElement || document.documentElement) as HTMLElement;
    const body = document.body;
    const xInfo = documentAxisCanScroll('x', root, body, viewport.width);
    const yInfo = documentAxisCanScroll('y', root, body, viewport.height);
    if (xInfo.can || yInfo.can) {
        const restores = [saveAndDisableSmoothScroll(root)];
        if (body && body !== root) {
            restores.push(saveAndDisableSmoothScroll(body));
        }
        scrollTargets.push({
            kind: 'window',
            canX: xInfo.can,
            canY: yInfo.can,
            originalX: scrollOffset(window).x,
            originalY: scrollOffset(window).y,
            maxScrollX: xInfo.maxScroll,
            maxScrollY: yInfo.maxScroll,
            restores,
        });
    }

    clipTargets.push({
        kind: 'viewport',
        clipsX: true,
        clipsY: true,
    });
    apertureWidth = Math.max(1, Math.min(apertureWidth, viewport.width));
    apertureHeight = Math.max(1, Math.min(apertureHeight, viewport.height));

    const canRevealX = scrollTargets.some((scrollTarget) => scrollTarget.canX);
    const canRevealY = scrollTargets.some((scrollTarget) => scrollTarget.canY);
    if (!canRevealX) {
        const next = intersectRectAxes(bounds, {x: -targetRect.left, y: 0, width: viewport.width, height: bounds.height}, true, false);
        bounds = next || emptyRect();
    }
    if (!canRevealY) {
        const next = intersectRectAxes(bounds, {x: 0, y: -targetRect.top, width: bounds.width, height: viewport.height}, false, true);
        bounds = next || emptyRect();
    }

    return {scrollTargets, clipTargets, bounds, apertureWidth, apertureHeight, selfScrollX, selfScrollY};
}

export async function beginElementScreenshot(target: Element | null): Promise<ScreenshotPlan> {
    if (!target || !target.isConnected) {
        throw new Error('element screenshot target is not connected');
    }
    const rect = target.getBoundingClientRect();
    if (!isFinitePositive(rect.width) || !isFinitePositive(rect.height)) {
        throw new Error('invalid element screenshot size');
    }
    const state = collectElementScreenshotState(target);
    if (!isFinitePositive(state.bounds.width) || !isFinitePositive(state.bounds.height)) {
        throw new Error('element screenshot target is fully clipped');
    }
    const width = state.bounds.width;
    const height = state.bounds.height;
    const tiles = makeTiles(width, height, Math.min(width, state.apertureWidth), Math.min(height, state.apertureHeight));
    const token = makeToken('element_shot');
    const overlayRestores = suspendInjectedOverlays();
    elementSessions.set(token, {
        token,
        target,
        originX: state.bounds.x,
        originY: state.bounds.y,
        width,
        height,
        tiles,
        scrollTargets: state.scrollTargets,
        clipTargets: state.clipTargets,
        overlayRestores,
        selfScrollX: state.selfScrollX,
        selfScrollY: state.selfScrollY,
    });
    return {token, width, height, tiles};
}

function targetScreenOffsetForLocal(session: ElementScreenshotSession, localX: number, localY: number): {x: number; y: number} {
    const targetElement = session.target instanceof HTMLElement ? session.target : null;
    if (!targetElement || (!session.selfScrollX && !session.selfScrollY)) {
        return {x: localX, y: localY};
    }
    return {
        x: targetElement.clientLeft + (session.selfScrollX ? localX - targetElement.scrollLeft : localX),
        y: targetElement.clientTop + (session.selfScrollY ? localY - targetElement.scrollTop : localY),
    };
}

function scrollElementTarget(session: ElementScreenshotSession, target: ScrollTarget, localX: number, localY: number): void {
    if (target.kind === 'self') {
        const element = target.element;
        if (!element) {
            return;
        }
        if (target.canX) {
            element.scrollLeft = clamp(localX, 0, target.maxScrollX);
        }
        if (target.canY) {
            element.scrollTop = clamp(localY, 0, target.maxScrollY);
        }
        return;
    }
    if (target.kind === 'window') {
        const rect = session.target.getBoundingClientRect();
        const current = scrollOffset(window);
        const targetOffset = targetScreenOffsetForLocal(session, localX, localY);
        window.scrollTo(
            target.canX ? clamp(current.x + rect.left + targetOffset.x, 0, target.maxScrollX) : target.originalX,
            target.canY ? clamp(current.y + rect.top + targetOffset.y, 0, target.maxScrollY) : target.originalY,
        );
        return;
    }
    const element = target.element;
    if (!element) {
        return;
    }
    const rect = session.target.getBoundingClientRect();
    const container = element.getBoundingClientRect();
    const targetOffset = targetScreenOffsetForLocal(session, localX, localY);
    if (target.canX) {
        element.scrollLeft = clamp(element.scrollLeft + rect.left + targetOffset.x - container.left - element.clientLeft, 0, target.maxScrollX);
    }
    if (target.canY) {
        element.scrollTop = clamp(element.scrollTop + rect.top + targetOffset.y - container.top - element.clientTop, 0, target.maxScrollY);
    }
}

function elementScreenshotSourceDest(session: ElementScreenshotSession, tile: ScreenshotTile, localX: number, localY: number): {source: ScreenshotRect; dest: ScreenshotRect} {
    const targetElement = session.target instanceof HTMLElement ? session.target : null;
    if (targetElement && (session.selfScrollX || session.selfScrollY)) {
        const client = elementClientRect(targetElement);
        const scrollLeft = session.selfScrollX ? targetElement.scrollLeft || 0 : 0;
        const scrollTop = session.selfScrollY ? targetElement.scrollTop || 0 : 0;
        const requestedLeft = localX;
        const requestedTop = localY;
        const requestedRight = Math.min(localX + tile.width, session.originX + session.width);
        const requestedBottom = Math.min(localY + tile.height, session.originY + session.height);
        const visibleLeft = session.selfScrollX ? Math.max(requestedLeft, scrollLeft) : requestedLeft;
        const visibleTop = session.selfScrollY ? Math.max(requestedTop, scrollTop) : requestedTop;
        const visibleRight = session.selfScrollX ? Math.min(requestedRight, scrollLeft + client.width) : requestedRight;
        const visibleBottom = session.selfScrollY ? Math.min(requestedBottom, scrollTop + client.height) : requestedBottom;
        if (visibleRight <= visibleLeft + RECT_EPSILON || visibleBottom <= visibleTop + RECT_EPSILON) {
            return {source: emptyRect(), dest: emptyRect()};
        }
        return {
            source: {
                x: client.x + (session.selfScrollX ? visibleLeft - scrollLeft : visibleLeft),
                y: client.y + (session.selfScrollY ? visibleTop - scrollTop : visibleTop),
                width: visibleRight - visibleLeft,
                height: visibleBottom - visibleTop,
            },
            dest: {
                x: visibleLeft - session.originX,
                y: visibleTop - session.originY,
                width: visibleRight - visibleLeft,
                height: visibleBottom - visibleTop,
            },
        };
    }

    const rect = session.target.getBoundingClientRect();
    const source = {
        x: rect.left + localX,
        y: rect.top + localY,
        width: Math.min(tile.width, session.width - tile.x),
        height: Math.min(tile.height, session.height - tile.y),
    };
    return {
        source,
        dest: {
            x: source.x - rect.left - session.originX,
            y: source.y - rect.top - session.originY,
            width: source.width,
            height: source.height,
        },
    };
}

function clipRectForTarget(target: ClipTarget): ScreenshotRect | null {
    if (target.kind === 'viewport') {
        const viewport = viewportSize(window);
        return {x: 0, y: 0, width: viewport.width, height: viewport.height};
    }
    if (!target.element) {
        return null;
    }
    return elementClientRect(target.element);
}

export async function applyElementScreenshotTile(target: Element | null, token: string, tile: ScreenshotTile, project: ScreenshotTileProjector): Promise<ScreenshotTileResult> {
    const session = elementSessions.get(token);
    if (!session || !target || target !== session.target || !target.isConnected) {
        throw new Error('element screenshot session not found');
    }
    const localX = session.originX + tile.x;
    const localY = session.originY + tile.y;
    const before = session.scrollTargets.map((scrollTarget) => {
        if (scrollTarget.kind === 'window') {
            return scrollOffset(window);
        }
        return {
            x: scrollTarget.element?.scrollLeft || 0,
            y: scrollTarget.element?.scrollTop || 0,
        };
    });
    for (const scrollTarget of session.scrollTargets) {
        scrollElementTarget(session, scrollTarget, localX, localY);
    }
    await settleScreenshotScroll();
    const freshFrame = session.scrollTargets.some((scrollTarget, index) => {
        const previous = before[index] || {x: 0, y: 0};
        const current = scrollTarget.kind === 'window'
            ? scrollOffset(window)
            : {
                x: scrollTarget.element?.scrollLeft || 0,
                y: scrollTarget.element?.scrollTop || 0,
            };
        return Math.abs(current.x - previous.x) > SCROLL_EPSILON || Math.abs(current.y - previous.y) > SCROLL_EPSILON;
    });

    let {source, dest} = elementScreenshotSourceDest(session, tile, localX, localY);
    for (const clipTarget of session.clipTargets) {
        const clip = clipRectForTarget(clipTarget);
        if (!clip) {
            continue;
        }
        const clipped = intersectRectAxes(source, clip, clipTarget.clipsX, clipTarget.clipsY);
        if (!clipped) {
            return {source: emptyRect(), dest: emptyRect(), freshFrame};
        }
        const missingRight = clipTarget.clipsX && tile.x + tile.width >= session.width - SCROLL_EPSILON
            ? Math.max(0, source.x + source.width - (clipped.x + clipped.width))
            : 0;
        const missingBottom = clipTarget.clipsY && tile.y + tile.height >= session.height - SCROLL_EPSILON
            ? Math.max(0, source.y + source.height - (clipped.y + clipped.height))
            : 0;
        dest = {
            x: dest.x + (clipped.x - source.x),
            y: dest.y + (clipped.y - source.y),
            width: clipped.width + (missingRight <= SCROLL_EPSILON ? missingRight : 0),
            height: clipped.height + (missingBottom <= SCROLL_EPSILON ? missingBottom : 0),
        };
        source = clipped;
    }
    const sourceWindow = session.target.ownerDocument.defaultView;
    const projected = projectRectToTopWindow(sourceWindow, source);
    if (!projected) {
        return project({source, dest, freshFrame});
    }
    return clipScreenshotTile({source: projected, dest, freshFrame}, currentTopViewportRect(sourceWindow || window));
}

export async function finishElementScreenshot(token: string): Promise<void> {
    const session = elementSessions.get(token);
    if (!session) {
        return;
    }
    elementSessions.delete(token);
    for (let i = session.scrollTargets.length - 1; i >= 0; i--) {
        const target = session.scrollTargets[i];
        if (target.kind === 'window') {
            window.scrollTo(target.originalX, target.originalY);
        } else if (target.element) {
            target.element.scrollLeft = target.originalX;
            target.element.scrollTop = target.originalY;
        }
        restoreStyles(target.restores);
    }
    restoreInjectedOverlays(session.overlayRestores);
    await settleScreenshotScroll();
}

function currentScrollXY(target: ScrollTarget): {x: number; y: number} {
    if (target.kind === 'window') {
        const offset = scrollOffset(window);
        return {x: offset.x, y: offset.y};
    }
    if (target.element) {
        return {x: target.element.scrollLeft, y: target.element.scrollTop};
    }
    return {x: 0, y: 0};
}

function setScrollXY(target: ScrollTarget, x: number, y: number): void {
    if (target.kind === 'window') {
        window.scrollTo(x, y);
    } else if (target.element) {
        target.element.scrollLeft = x;
        target.element.scrollTop = y;
    }
}

export async function elementScrollNext(target: Element, axis: Axis, direction: 1 | -1): Promise<boolean> {
    if (!target || !target.isConnected) {
        return false;
    }
    const state = collectElementScreenshotState(target);
    const step = axis === 'y' ? state.apertureHeight : state.apertureWidth;

    for (const scrollTarget of state.scrollTargets) {
        const canAxis = axis === 'y' ? scrollTarget.canY : scrollTarget.canX;
        if (!canAxis) continue;

        const current = currentScrollXY(scrollTarget);
        const currentPos = axis === 'y' ? current.y : current.x;
        const maxScroll = axis === 'y' ? scrollTarget.maxScrollY : scrollTarget.maxScrollX;

        if (direction > 0) {
            if (currentPos >= maxScroll - SCROLL_EPSILON) continue;
        } else {
            if (currentPos <= SCROLL_EPSILON) continue;
        }

        const before = currentPos;
        const desired = direction > 0
            ? Math.min(before + step, maxScroll)
            : Math.max(before - step, 0);

        if (axis === 'y') {
            setScrollXY(scrollTarget, current.x, desired);
        } else {
            setScrollXY(scrollTarget, desired, current.y);
        }

        const after = currentScrollXY(scrollTarget);
        const afterPos = axis === 'y' ? after.y : after.x;
        const moved = Math.abs(afterPos - before) > SCROLL_EPSILON;
        if (moved) await settleScreenshotScroll();
        return moved;
    }
    return false;
}

export async function elementScrollTo(target: Element, xRatio: number, yRatio: number): Promise<void> {
    if (!target || !target.isConnected) return;
    const state = collectElementScreenshotState(target);
    const elementRect = target.getBoundingClientRect();

    for (const scrollTarget of state.scrollTargets) {
        const container = scrollTarget.kind === 'window' ? document.documentElement : scrollTarget.element;
        if (!container) continue;
        const containerRect = container.getBoundingClientRect();

        if (scrollTarget.canY && yRatio >= 0) {
            const current = currentScrollXY(scrollTarget);
            const offset = elementRect.top - containerRect.top;
            const targetScroll = current.y + offset - (containerRect.height - elementRect.height) * yRatio;
            const clamped = Math.max(0, Math.min(targetScroll, scrollTarget.maxScrollY));
            setScrollXY(scrollTarget, current.x, clamped);
        }

        if (scrollTarget.canX && xRatio >= 0) {
            const current = currentScrollXY(scrollTarget);
            const offset = elementRect.left - containerRect.left;
            const targetScroll = current.x + offset - (containerRect.width - elementRect.width) * xRatio;
            const clamped = Math.max(0, Math.min(targetScroll, scrollTarget.maxScrollX));
            setScrollXY(scrollTarget, clamped, current.y);
        }
        break;
    }
    await settleScreenshotScroll();
}
