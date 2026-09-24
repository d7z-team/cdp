import type {FrameElement} from "../../utils/frame_element";
import {registerFrameIdentity, registerFrameWindow} from "../../bridge/frame_identity";
import {shadowRootFor, registerShadowRoots, frameElements} from "../../dom/shadow";
import {compileRawSelectorPlan} from "../../query/plan";
import type {SelectorPlan} from "../../query/plan";
import {createCanvas} from "../../canvas";
import type {ICdpCanvas} from "../../canvas/types";
import {createRun} from "../../run";
import type {ICdpRun} from "../../run";
import {describeElement, createLocator} from "../../locator";
import type {ActionabilityDiagnostic, ActionabilityOptions} from "../../locator";
import {cloneDiagnostic, makeUnavailableFrameDiagnostic, applyFrameElementDiagnostic} from "../../locator/frame";
import {domReady} from "../../utils/dom";
import {CallCore} from "../../bindings/core";
import {shouldSkipBootstrapInCurrentFrame} from "../../utils/internal_ids";
import {withFrameTag} from "../../utils/frame_log";
import {
    applyElementScreenshotTile,
    applyFrameDocumentScreenshotTile,
    applyPageScreenshotTile,
    beginElementScreenshot,
    beginFrameDocumentScreenshot,
    beginPageScreenshot,
    elementScrollNext,
    elementScrollTo,
    finishElementScreenshot,
    finishFrameDocumentScreenshot,
    finishPageScreenshot,
    restoreInjectedOverlays,
    projectChildScreenshotTile,
    ScreenshotPlan,
    ScreenshotTile,
    ScreenshotTileResult,
    suspendInjectedOverlays,
} from "../../screenshot";
import {
    IndexedQueryRect,
    QueryRect,
    QueryRectCoordinateSpace,
    SelectorBridge,
    SelectorQueryBucket,
    SelectorQueryNodeInfo,
    SelectorQueryOptions,
    SelectorQueryRuntime,
    SelectorQueryFailure,
    SelectorQuerySession,
} from "../../query/runtime";
import {BrowserErrorNode, makeBrowserErrorNode} from "../../error/browser_error";

import {captureSnapshot, DOMRevision, SnapshotRegistry} from "../../snapshot";
import type {SnapshotCaptureRequest, SnapshotDocument} from "../../snapshot";
import {DEFAULT_HIGHLIGHT_COLOR, DEFAULT_HIGHLIGHT_TIMEOUT_MS} from "../../canvas/types";
import type {ICdpLocator} from "../../api/types";
import {FrameBridge, Channel} from "../../bridge";
import {clearCdpFFI, getCdpFFI} from "../../runtime/globals";
import {ensureRuntimeStarted, markRuntimeDestroyed} from "../../runtime/lifecycle";
import {MouseVisualController} from "../../pointer";
import type {MouseVisualAction} from "../../pointer";

const LOG_PREFIX = "[CDP CORE]";
export const CORE_HELPER_VERSION = "core-2026-06-04-screencast-screenshot-v1";
const HIGHLIGHT_DRAW_OWNER = 'page-highlight' as const;
const MAX_HIGHLIGHT_RECTS = 256;
const MAX_HIGHLIGHT_RECTS_PER_SELECTOR = 64;

function selectorQueryFailure(
    kind: SelectorQueryFailure['kind'],
    summary: string,
    detail?: string,
    frameSummary?: string,
    cause?: BrowserErrorNode,
): SelectorQueryFailure {
    return {
        kind,
        summary,
        detail,
        frameSummary,
        error: makeBrowserErrorNode({
            op: 'selector.frame_bridge',
            kind,
            message: summary,
            detail,
            frameSummary,
            cause,
        }),
    };
}

interface HighlightOptions {
    color?: string;
    modeName?: string;
    timeoutMs?: number;
    trackOnScroll?: boolean;
    everyFrameRoot?: boolean;
}

interface ActiveHighlightRequest {
    selectorInput: string[];
    options: HighlightOptions;
}



export class CdpFFI {
    public readonly version = CORE_HELPER_VERSION;
    private readonly runtimeId = `runtime_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
    public canvas!: ICdpCanvas;
    public locator!: ICdpLocator;
    public run!: ICdpRun;
    public bridge!: FrameBridge;
    private call!: CallCore
    private focusHandler?: () => void;
    private readonly boundHighlightViewportChange: () => void;
    private highlightViewportEventsBound = false;
    private highlightRefreshPending = false;
    private highlightRefreshInFlight = false;
    private highlightRefreshRerunRequested = false;
    private activeHighlightRequest: ActiveHighlightRequest | null = null;
    private highlightRevision = 0;
    private readonly screenshotOverlaySuspensions = new Map<string, ReturnType<typeof suspendInjectedOverlays>>();
    private queryRuntime!: SelectorQueryRuntime;
    private pointer?: MouseVisualController;
    private readonly snapshotRegistry = new SnapshotRegistry();
    private readonly domRevision = new DOMRevision();
    private bootstrapped = false;
    private ready = false;
    private destroying = false;
    private documentRoot = document.documentElement;
    public readonly _runtimeId: string;

    public constructor(private readonly runtimeGeneration = 0) {
        this._runtimeId = this.runtimeId;
        this.boundHighlightViewportChange = this.handleHighlightViewportChange.bind(this);
    }

    public registerFrameIdentity(id: string, parentID: string): void { registerFrameIdentity(id, parentID); }
    public registerFrameWindow(frame: FrameElement, id: string): void { registerFrameWindow(frame, id); }

    public registerShadowRoots(roots: ShadowRoot[]): void { registerShadowRoots(roots); }

    public async bootstrap() {
        if (this.ready || this.destroying) {
            return;
        }
        if (shouldSkipBootstrapInCurrentFrame()) {
            console.log(withFrameTag(`${LOG_PREFIX} bootstrap skipped for internal frame`));
            return;
        }
        console.log(withFrameTag(`${LOG_PREFIX} bootstrap start`));
        this.call = new CallCore();
        if (!this.isRuntimeGeneration(this.runtimeGeneration)) {
            return;
        }
        this.bridge = new FrameBridge(this.runtimeId);
        this.canvas = createCanvas(this.bridge);
        if (window.parent === window) {
            this.pointer = new MouseVisualController();
        }
        this.locator = createLocator();
        this.queryRuntime = new SelectorQueryRuntime({
            ...this.locator,
            checkActionability: (el, options) => this.actionabilityDiagnostic(el, options),
        }, {
            runtimeId: this.runtimeId,
            queryChildFrame: async (frameElement, plan, options) => {
                const cw = frameElement.contentWindow;
                const frameSummary = describeElement(frameElement);
                if (!cw) {
                    return {
                        session: null,
                        failure: selectorQueryFailure(
                            'frame_runtime_unavailable',
                            'Frame runtime unavailable',
                            'child frame has no reachable contentWindow',
                            frameSummary,
                        ),
                    };
                }
                try {
                    return await this.bridge.request(Channel.SELECTOR_QUERY, {plan, options}, cw);
                } catch (e) {
                    const detail = e instanceof Error ? e.message : String(e);
                    return {
                        session: null,
                        failure: selectorQueryFailure('bridge_timeout', 'Frame query unavailable', detail, frameSummary),
                    };
                }
            },
            collectRemoteRects: async (runtimeId, token, bucket, indices, coordinateSpace) => {
                const win = this.bridge.findWindowByRuntimeId(runtimeId);
                if (!win) return [];
                return this.bridge.request(Channel.SELECTOR_RECTS, {token, bucket, indices, coordinateSpace}, win);
            },
            disposeRemoteQuery: (runtimeId, token) => {
                const win = this.bridge.findWindowByRuntimeId(runtimeId);
                if (!win) return;
                void this.bridge.request(Channel.SELECTOR_QUERY_DISPOSE, {token}, win).catch(() => undefined);
            },
        } satisfies SelectorBridge);
        this.run = createRun();

        this.bridge.onRequest(Channel.SELECTOR_QUERY, async (payload: {plan: SelectorPlan; options: SelectorQueryOptions}) => {
            const session = await this.queryRuntime.startQuery(document, payload.plan, payload.options);
            return {session};
        });

        this.bridge.onRequest(Channel.SELECTOR_RECTS, async (payload: {token: string; bucket: SelectorQueryBucket; indices: number[]; coordinateSpace: QueryRectCoordinateSpace}) => {
            const projectToTop = payload.coordinateSpace === 'top' && window.parent !== window;
            const rects = await this.selectorQueryIndexedRects(
                payload.token,
                payload.bucket,
                payload.indices,
                projectToTop ? 'local' : payload.coordinateSpace,
            );
            if (projectToTop && rects.length > 0) {
                return await this.bridge.request<IndexedQueryRect[], IndexedQueryRect[]>(
                    Channel.SELECTOR_PROJECT_RECTS,
                    rects,
                    window.parent,
                );
            }
            return rects;
        });

        this.bridge.onRequest(Channel.SELECTOR_PROJECT_RECTS, async (rects: IndexedQueryRect[], event) => {
            const frame = this.bridge.findFrameElementByWindow(event.source as Window);
            if (!frame || !Array.isArray(rects)) return [];
            const frameRect = frame.getBoundingClientRect();
            const projected = rects.map(item => ({
                index: item.index,
                rect: {
                    x: item.rect.x + frameRect.left + frame.clientLeft,
                    y: item.rect.y + frameRect.top + frame.clientTop,
                    width: item.rect.width,
                    height: item.rect.height,
                },
            }));
            if (window.parent === window) return projected;
            return await this.bridge.request<IndexedQueryRect[], IndexedQueryRect[]>(
                Channel.SELECTOR_PROJECT_RECTS,
                projected,
                window.parent,
            );
        });

        this.bridge.onRequest(Channel.SCREENSHOT_PROJECT_TILE, async (tile: ScreenshotTileResult, event) => {
            const frame = this.bridge.findFrameElementByWindow(event.source as Window);
            if (!frame) throw new Error('screenshot frame owner is unavailable');
            const projected = projectChildScreenshotTile(frame, tile);
            return window.parent === window ? projected : this.projectScreenshotTile(projected);
        });

        this.bridge.onRequest(Channel.HIGHLIGHT_FRAME_RECTS, async (payload: {selector: string; limit: number}) => {
            return await this.resolveSelectorRectsAcrossFrameRoots(payload.selector, payload.limit);
        });

        this.bridge.onRequest(Channel.SELECTOR_QUERY_DISPOSE, async (payload: {token?: string}) => {
            this.queryRuntime.disposeQuery(payload.token);
            return true;
        });

        this.bridge.onRequest(Channel.ACTIONABILITY, async (payload: {diagnostic: ActionabilityDiagnostic; options?: ActionabilityOptions}, event) => {
            const srcWin = event.source as Window;
            const frameElement = this.bridge.findFrameElementByWindow(srcWin);
            if (!frameElement) {
                return {diagnostic: makeUnavailableFrameDiagnostic(payload.diagnostic, 'Unable to resolve child frame element in parent document')};
            }
            const merged = await applyFrameElementDiagnostic(frameElement, payload.diagnostic, payload.options);
            if (!merged.actionable || window.parent === window) {
                return {diagnostic: merged};
            }
            try {
                return await this.bridge.request(Channel.ACTIONABILITY, {diagnostic: merged, options: payload.options}, window.parent);
            } catch {
                return {diagnostic: makeUnavailableFrameDiagnostic(merged, 'ancestor frame did not respond to actionability diagnostic request')};
            }
        });

        this.bridge.onRequest(Channel.SNAPSHOT_CAPTURE, async (payload: SnapshotCaptureRequest) => {
            return this.captureSnapshot(payload);
        });

        this.bridge.onRequest(Channel.SNAPSHOT_QUIET, async (payload: {quiet_ms: number; timeout_ms: number}) => {
            return this.waitForSnapshotQuiet(payload.quiet_ms, payload.timeout_ms);
        });

        this.focusHandler = () => {
            try {
                this.call.onFocus();
            } catch (e) {
                console.error(withFrameTag(`${LOG_PREFIX} focus sync failed:`), e);
            }
        };
        window.addEventListener('focus', this.focusHandler);
        if (document.hasFocus()) {
            void Promise.resolve(this.call.onFocus()).catch((error) => {
                console.warn(withFrameTag(`${LOG_PREFIX} initial focus sync failed:`), error);
            });
        }
        if (!this.isRuntimeGeneration(this.runtimeGeneration)) {
            return;
        }
        this.bootstrapped = true;
        this.ready = true;
        this.announceRuntimeReady();
        console.log(withFrameTag(`${LOG_PREFIX} bootstrap complete`));
    }

    public runtimeReady(): boolean {
        return this.ready && !this.destroying;
    }

    public isCurrentDocument(): boolean {
        // document.open replaces the DOM and removes listeners without replacing
        // this execution context. Rebuild the facade when that document changes.
        this.documentRoot ??= document.documentElement;
        return this.documentRoot === document.documentElement;
    }

    public announceRuntimeReady(): void {
        if (this.runtimeReady()) this.call.coreRuntimeReady(window.parent === window);
    }

    public setRuntimeReady(): void {
        this.ready = true;
    }

    public showMouseAction(action: MouseVisualAction): void {
        this.pointer?.perform(action);
    }

    public isRuntimeGeneration(generation: number): boolean {
        return this.runtimeGeneration === generation && getCdpFFI<CdpFFI>() === this && !this.destroying;
    }

    public destroy() {
        if (this.destroying) {
            return;
        }
        this.destroying = true;
        console.log(withFrameTag(`${LOG_PREFIX} cleanup start`));
        const failures: unknown[] = [];
        const cleanup = (name: string, action: () => void) => {
            try {
                action();
            } catch (error) {
                failures.push(error);
                console.warn(withFrameTag(`${LOG_PREFIX} failed to clean ${name}:`), error);
            }
        };
        const focusHandler = this.focusHandler;
        if (focusHandler) cleanup('focus listener', () => {
            window.removeEventListener('focus', focusHandler);
            this.focusHandler = undefined;
        });
        cleanup('bridge', () => {
            this.bridge?.destroy();
        });
        cleanup('binding', () => this.call?.destroy?.());
        cleanup('run runtime', () => this.run?.destroy?.());
        cleanup('pointer', () => this.pointer?.destroy());
        cleanup('highlight tracking', () => this.clearActiveHighlightTracking());
        cleanup('selector query runtime', () => this.queryRuntime?.clear());
        cleanup('snapshot registry', () => this.snapshotRegistry.dispose());
        cleanup('DOM revision tracker', () => this.domRevision.destroy());
        cleanup('canvas', () => this.canvas?.destroy());
        this.bootstrapped = false;
        this.ready = false;
        clearCdpFFI(this);
        markRuntimeDestroyed('ffi', this);
        console.log(withFrameTag(`${LOG_PREFIX} cleanup complete`));
        if (failures.length > 0) {
            throw new AggregateError(failures, 'CDP core runtime cleanup failed');
        }
    }


    public helperInfo() {
        return {
            version: this.version,
            bootstrapped: this.bootstrapped,
            runtimeId: this.runtimeId,
        };
    }

    public runtimeInfo() {
        return {
            runtimeId: this.runtimeId,
        };
    }

    public async startSelectorQuery(root: Element | Document | ShadowRoot, plan: SelectorPlan, options: SelectorQueryOptions = {}): Promise<SelectorQuerySession> {
        return this.queryRuntime.startQuery(root, plan, options);
    }

    public validateSelectorPlan(root: Element | Document | ShadowRoot, plan: SelectorPlan): void {
        this.queryRuntime.validatePlan(root, plan);
    }

    public selectorQueryNode(token: string, bucket: SelectorQueryBucket, index: number): Element | null {
        return this.queryRuntime.queryNode(token, bucket, index);
    }

    public selectorQueryNodeInfo(token: string, bucket: SelectorQueryBucket, index: number): SelectorQueryNodeInfo | null {
        return this.queryRuntime.queryNodeInfo(token, bucket, index);
    }

    public selectorMembershipElements(key: string): Element[] {
        return this.queryRuntime.membershipElements(key);
    }

    public selectorMembershipContains(key: string, element: Element): boolean {
        return this.queryRuntime.elementInMembership(key, element);
    }

    public async beginPageScreenshot(): Promise<ScreenshotPlan> {
        return beginPageScreenshot();
    }

    public async applyPageScreenshotTile(token: string, tile: ScreenshotTile): Promise<ScreenshotTileResult> {
        return applyPageScreenshotTile(token, tile);
    }

    public async finishPageScreenshot(token: string): Promise<void> {
        return finishPageScreenshot(token);
    }

    public suspendInjectedOverlaysForScreenshot(): string {
        const token = `shot_overlay_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
        this.screenshotOverlaySuspensions.set(token, suspendInjectedOverlays());
        return token;
    }

    public restoreInjectedOverlaysForScreenshot(token: string): boolean {
        const restores = this.screenshotOverlaySuspensions.get(token);
        if (!restores) {
            return false;
        }
        this.screenshotOverlaySuspensions.delete(token);
        restoreInjectedOverlays(restores);
        return true;
    }

    public async beginElementScreenshot(target: Element | null): Promise<ScreenshotPlan> {
        return beginElementScreenshot(target);
    }

    public async applyElementScreenshotTile(target: Element | null, token: string, tile: ScreenshotTile): Promise<ScreenshotTileResult> {
        return applyElementScreenshotTile(target, token, tile, result => this.projectScreenshotTile(result));
    }

    private projectScreenshotTile(tile: ScreenshotTileResult): Promise<ScreenshotTileResult> {
        return this.bridge.request(Channel.SCREENSHOT_PROJECT_TILE, tile, window.parent);
    }

    public async finishElementScreenshot(token: string): Promise<void> {
        return finishElementScreenshot(token);
    }

    public async beginFrameDocumentScreenshot(target: Element | null): Promise<ScreenshotPlan> {
        return beginFrameDocumentScreenshot(target);
    }

    public async applyFrameDocumentScreenshotTile(target: Element | null, token: string, tile: ScreenshotTile): Promise<ScreenshotTileResult> {
        return applyFrameDocumentScreenshotTile(target, token, tile, result => this.projectScreenshotTile(result));
    }

    public async finishFrameDocumentScreenshot(token: string): Promise<void> {
        return finishFrameDocumentScreenshot(token);
    }

    public async elementScrollNext(target: Element | null, axis: 'x' | 'y', direction: 1 | -1): Promise<boolean> {
        return target ? elementScrollNext(target, axis, direction) : false;
    }

    public async elementScrollTo(target: Element | null, x: number, y: number): Promise<void> {
        if (target) await elementScrollTo(target, x, y);
    }

    public resolveUploadFileInput(target: Element | null): HTMLInputElement {
        const candidates: HTMLInputElement[] = [];
        const seen = new Set<HTMLInputElement>();
        const addCandidate = (node: Element | null | undefined) => {
            if (!(node instanceof HTMLInputElement)) {
                return;
            }
            if ((node.type || '').toLowerCase() !== 'file' || seen.has(node)) {
                return;
            }
            seen.add(node);
            candidates.push(node);
        };
        const addLabelControl = (node: Element | null | undefined) => {
            if (node instanceof HTMLLabelElement) {
                addCandidate(node.control);
            }
        };

        if (target) {
            addCandidate(target);
            target.querySelectorAll('input').forEach(addCandidate);
            addLabelControl(target);
            addLabelControl(target.closest('label'));
            target.querySelectorAll('label').forEach(addLabelControl);
            addCandidate(target.previousElementSibling);
            addCandidate(target.nextElementSibling);

            const ownerDocument = target.ownerDocument || document;
            const forTarget = target.getAttribute('for')?.trim();
            if (forTarget) {
                addCandidate(ownerDocument.getElementById(forTarget));
            }
            for (const id of (target.getAttribute('aria-controls') || '').trim().split(/\s+/)) {
                if (id) {
                    addCandidate(ownerDocument.getElementById(id));
                }
            }
        }

        if (candidates.length === 1) {
            return candidates[0];
        }
        if (candidates.length === 0) {
            throw new Error('上传目标不是 input[type=file]，且未找到关联的文件输入框');
        }
        throw new Error(`上传目标不是唯一的文件输入框，推导到 ${candidates.length} 个 input[type=file]，请直接定位具体上传 input`);
    }

    public async selectorQueryRects(
        token: string,
        bucket: SelectorQueryBucket,
        indices: number[],
        coordinateSpace: QueryRectCoordinateSpace = 'top',
    ): Promise<QueryRect[]> {
        if (indices.length > 0) {
            return (await this.selectorQueryIndexedRects(token, bucket, indices, coordinateSpace))
                .map((item) => item.rect);
        }
        const rects = await this.queryRuntime.collectRects(token, bucket, coordinateSpace, 0);
        return rects;
    }

    private async selectorQueryIndexedRects(
        token: string,
        bucket: SelectorQueryBucket,
        indices: number[],
        coordinateSpace: QueryRectCoordinateSpace = 'top',
    ): Promise<IndexedQueryRect[]> {
        const rects = await this.queryRuntime.collectIndexedRects(token, bucket, indices, coordinateSpace);
        if (indices.length === 0) {
            return rects;
        }
        const byIndex = new Map(rects.map((item) => [item.index, item] as const));
        return indices
            .map((index) => byIndex.get(index))
            .filter((item): item is IndexedQueryRect => !!item);
    }

    public disposeSelectorQuery(token?: string) {
        this.queryRuntime.disposeQuery(token);
    }

    public async actionabilityDiagnostic(el: Element | null, options?: ActionabilityOptions): Promise<ActionabilityDiagnostic> {
        if (!el) {
            return {
                actionable: false,
                kind: 'not_found',
                summary: 'Element not provided',
                detail: '',
                element: 'unknown',
                frameChain: [],
            };
        }
        const local = await this.locator.checkActionability(el, options);
        if ((!local.actionable && !local.retriable) || window.parent === window) {
            if (local.localRect && !local.topRect) {
                local.topRect = {...local.localRect};
            }
            return local;
        }
        try {
            const res = await this.bridge.request<{diagnostic: ActionabilityDiagnostic; options?: ActionabilityOptions}, {diagnostic: ActionabilityDiagnostic}>(
                Channel.ACTIONABILITY,
                {diagnostic: cloneDiagnostic(local), options},
                window.parent,
            );
            return res.diagnostic;
        } catch {
            return {
                ...cloneDiagnostic(local),
                actionable: false,
                kind: 'cross_origin_unreachable',
                summary: 'Frame diagnostic unavailable',
                detail: 'parent frame did not respond to actionability diagnostic request',
            };
        }
    }

    public async highlight(selectorInput: string[], options: HighlightOptions = {}) {
        await domReady();
        const revision = ++this.highlightRevision;
        const color = options.color ?? DEFAULT_HIGHLIGHT_COLOR;
        const modeName = options.modeName ?? '高亮目标';
        const timeoutMs = typeof options.timeoutMs === 'number' ? options.timeoutMs : DEFAULT_HIGHLIGHT_TIMEOUT_MS;
        const drawOptions = {owner: HIGHLIGHT_DRAW_OWNER, revision, color, modeName, timeoutMs};
        this.canvas.clear({owner: HIGHLIGHT_DRAW_OWNER, revision});

        const allRects: Array<QueryRect & {radius: number}> = [];
        const seenRects = new Set<string>();
        for (const selector of selectorInput) {
            if (allRects.length >= MAX_HIGHLIGHT_RECTS) break;
            try {
                const limit = Math.min(MAX_HIGHLIGHT_RECTS_PER_SELECTOR, MAX_HIGHLIGHT_RECTS - allRects.length);
                const rects = options.everyFrameRoot
                    ? await this.resolveSelectorRectsAcrossFrameRoots(selector, limit)
                    : await this.resolveSelectorRects(selector, limit);
                for (const rect of rects) {
                    const key = `${Math.round(rect.x)}:${Math.round(rect.y)}:${Math.round(rect.width)}:${Math.round(rect.height)}`;
                    if (seenRects.has(key)) continue;
                    seenRects.add(key);
                    allRects.push({...rect, radius: 0});
                    if (allRects.length >= MAX_HIGHLIGHT_RECTS) break;
                }
            } catch {
                // Highlight is best effort; another fallback may still have rectangles.
            }
        }
        if (allRects.length > 0) {
            this.canvas.drawMultiple(allRects, drawOptions);
        }
        if (options.trackOnScroll) {
            this.activeHighlightRequest = {
                selectorInput: [...selectorInput],
                options: {...options},
            };
            this.bindHighlightTracking();
            return;
        }
        this.clearActiveHighlightTracking();
    }

    private bindHighlightTracking() {
        if (this.highlightViewportEventsBound) {
            return;
        }
        window.addEventListener('scroll', this.boundHighlightViewportChange, true);
        window.addEventListener('resize', this.boundHighlightViewportChange, true);
        window.visualViewport?.addEventListener('scroll', this.boundHighlightViewportChange);
        window.visualViewport?.addEventListener('resize', this.boundHighlightViewportChange);
        this.highlightViewportEventsBound = true;
    }

    private clearActiveHighlightTracking() {
        this.activeHighlightRequest = null;
        this.highlightRefreshPending = false;
        this.highlightRefreshRerunRequested = false;
        if (!this.highlightViewportEventsBound) {
            return;
        }
        window.removeEventListener('scroll', this.boundHighlightViewportChange, true);
        window.removeEventListener('resize', this.boundHighlightViewportChange, true);
        window.visualViewport?.removeEventListener('scroll', this.boundHighlightViewportChange);
        window.visualViewport?.removeEventListener('resize', this.boundHighlightViewportChange);
        this.highlightViewportEventsBound = false;
    }

    private handleHighlightViewportChange() {
        if (!this.activeHighlightRequest) {
            return;
        }
        if (this.highlightRefreshInFlight) {
            this.highlightRefreshRerunRequested = true;
            return;
        }
        if (this.highlightRefreshPending) {
            return;
        }
        this.highlightRefreshPending = true;
        window.requestAnimationFrame(() => {
            this.highlightRefreshPending = false;
            void this.runActiveHighlightRefresh();
        });
    }

    private async runActiveHighlightRefresh() {
        const activeRequest = this.activeHighlightRequest;
        if (!activeRequest || this.highlightRefreshInFlight) {
            return;
        }

        this.highlightRefreshInFlight = true;
        try {
            await this.highlight(activeRequest.selectorInput, activeRequest.options);
        } catch (e) {
        } finally {
            this.highlightRefreshInFlight = false;
        }

        if (!this.activeHighlightRequest || !this.highlightRefreshRerunRequested) {
            return;
        }
        this.highlightRefreshRerunRequested = false;
        this.handleHighlightViewportChange();
    }

    private async resolveSelectorRects(selector: string, limit: number): Promise<Array<{x: number; y: number; width: number; height: number}>> {
        const session = await this.queryRuntime.startQuery(document, compileRawSelectorPlan(selector), {
            mode: 'load',
            resultMode: 'sample',
            limit,
        });
        try {
            return this.queryRuntime.collectRects(
                session.token,
                'ids',
                window.parent === window ? 'top' : 'local',
                limit,
            );
        } finally {
            this.queryRuntime.disposeQuery(session.token);
        }
    }

    private async resolveSelectorRectsAcrossFrameRoots(selector: string, limit: number): Promise<QueryRect[]> {
        if (limit <= 0) return [];
        const session = await this.queryRuntime.startQuery(document, compileRawSelectorPlan(selector), {
            mode: 'load',
            resultMode: 'sample',
            limit,
            allowImplicitFrameFallback: false,
        });
        let rects: QueryRect[];
        try {
            rects = await this.queryRuntime.collectRects(session.token, 'ids', 'local', limit);
        } finally {
            this.queryRuntime.disposeQuery(session.token);
        }
        for (const frame of frameElements()) {
            if (rects.length >= limit) break;
            const target = (frame as HTMLIFrameElement | HTMLFrameElement).contentWindow;
            if (!target) continue;
            try {
                const childRects = await this.bridge.request<{selector: string; limit: number}, QueryRect[]>(
                    Channel.HIGHLIGHT_FRAME_RECTS,
                    {selector, limit: limit - rects.length},
                    target,
                );
                const frameRect = frame.getBoundingClientRect();
                rects.push(...childRects.map(rect => ({
                    x: rect.x + frameRect.left + frame.clientLeft,
                    y: rect.y + frameRect.top + frame.clientTop,
                    width: rect.width,
                    height: rect.height,
                })));
            } catch {
            }
        }
        return rects.slice(0, limit);
    }

    public async captureSnapshot(request: SnapshotCaptureRequest): Promise<SnapshotDocument> {
        this.domRevision.start();
        return captureSnapshot(request, this.runtimeId, this.snapshotRegistry, this.domRevision.current(), async (frame, childRequest) => {
            const childWindow = frame.contentWindow;
            if (!childWindow) throw new Error('frame has no content window');
            return this.bridge.request(Channel.SNAPSHOT_CAPTURE, childRequest, childWindow, 5000);
        });
    }

    public snapshotNode(snapshotId: number, elementId: number): Element | null {
        return this.snapshotRegistry.resolve(snapshotId, elementId);
    }

    public async waitForSnapshotQuiet(quietMs = 250, timeoutMs = 5000): Promise<{revision: number; timed_out: boolean}> {
        quietMs = Math.max(0, quietMs);
        timeoutMs = Math.max(quietMs, timeoutMs);
        const frames = new Set<FrameElement>();
        const roots: Array<Document | ShadowRoot> = [document];
        for (let index = 0; index < roots.length; index++) {
            const root = roots[index];
            for (const frame of Array.from(root.querySelectorAll('iframe, frame'))) {
                frames.add(frame as FrameElement);
            }
            for (const element of Array.from(root.querySelectorAll('*'))) {
                const shadow = shadowRootFor(element);
                if (shadow) roots.push(shadow);
            }
        }

        const local = this.domRevision.waitForQuiet(quietMs, timeoutMs);
        const children = Array.from(frames, async frame => {
            const childWindow = frame.contentWindow;
            if (!childWindow) return {revision: 0, timed_out: true};
            try {
                return await this.bridge.request<{quiet_ms: number; timeout_ms: number}, {revision: number; timed_out: boolean}>(
                    Channel.SNAPSHOT_QUIET,
                    {quiet_ms: quietMs, timeout_ms: timeoutMs},
                    childWindow,
                    timeoutMs + 250,
                );
            } catch {
                return {revision: 0, timed_out: true};
            }
        });
        const settled = Promise.all([local, ...children]);
        const deadline = new Promise<Array<{revision: number; timed_out: boolean}>>(resolve => {
            window.setTimeout(() => resolve([{revision: this.domRevision.current(), timed_out: true}]), timeoutMs);
        });
        const results = await Promise.race([settled, deadline]);
        return {
            revision: this.domRevision.current(),
            timed_out: results.some(result => result.timed_out),
        };
    }


}

void ensureRuntimeStarted(
    'ffi',
    generation => new CdpFFI(generation),
    instance => instance.bootstrap(),
    error => console.warn(withFrameTag(`${LOG_PREFIX} bootstrap failed:`), error),
).then(instance => instance?.bridge.addTransportKey("__FRAME_TRANSPORT_KEY__"));
