import {ownerFrameElement} from "../bridge/frame_identity";
import {shadowRootFor} from "../dom/shadow";
import {parseSelectorPath} from "./plan";
import type {ParsedSelectorSegment, SelectorPlan, SelectorPlanLayer, SelectorPlanTerminal} from "./plan";
import type { ActionabilityDiagnostic, ActionabilityOptions, CdpLocator } from "../locator";
import { visualBoxForElement } from "../locator/visual_box";
import { getByHasText, getByLabel, getByPlaceholder, getByRole, getByTestId, getByText } from "../locator/query";
import { isFrameElement } from "../utils/frame_element";
import { BrowserErrorNode, makeBrowserErrorNode, withBrowserErrorFrameSummary } from "../error/browser_error";
import { evaluateXPathElements, isDocumentAbsoluteXPath } from "./xpath_engine";
import {closestInternalElement, isInternalElement} from "../utils/internal_ui";

export type SelectorResolveMode = 'load' | 'actionable';
export type SelectorQueryResultMode = 'count' | 'sample' | 'full';
export type SelectorQueryBucket = 'ids' | 'visible' | 'actionable';
export type QueryRectCoordinateSpace = 'local' | 'top';

export interface SelectorQueryOptions {
    limit?: number;
    mode?: SelectorResolveMode;
    resultMode?: SelectorQueryResultMode;
    actionMode?: ActionabilityOptions['mode'];
    actionPurpose?: ActionabilityOptions['purpose'];
    allowImplicitFrameFallback?: boolean;
    membershipKey?: string;
}

export interface SelectorQueryBucketInfo {
    count: number;
}

export interface SelectorQuerySession {
    token: string;
    runtimeId: string;
    ids: SelectorQueryBucketInfo;
    visible: SelectorQueryBucketInfo;
    actionable: SelectorQueryBucketInfo;
    truncated: boolean;
    usedImplicitFrameFallback?: boolean;
    implicitFrameElementSummary?: string;
    failure?: SelectorQueryFailure;
}

export interface SelectorQueryFailure {
    kind: 'frame_runtime_unavailable' | 'cross_origin_unreachable' | 'bridge_timeout' | 'syntax' | 'no_match';
    summary: string;
    detail?: string;
    frameSummary?: string;
    error?: BrowserErrorNode;
}

export interface SelectorQueryAttempt {
    selector?: string;
    kind: 'empty' | SelectorQueryFailure['kind'];
    summary: string;
    detail?: string;
    frameSummary?: string;
}

export interface QueryRect {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface IndexedQueryRect {
    index: number;
    rect: QueryRect;
}

export interface QueryPoint {
    x: number;
    y: number;
}

export interface SelectorQueryNodeInfo {
    kind: 'local' | 'remote';
    runtimeId: string;
    token: string;
    bucket: SelectorQueryBucket;
    index: number;
}

export interface SelectorBridge {
    runtimeId: string;
    queryChildFrame(
        frameElement: HTMLIFrameElement | HTMLFrameElement,
        plan: SelectorPlan,
        options: SelectorQueryOptions,
    ): Promise<{session: SelectorQuerySession | null; failure?: SelectorQueryFailure}>;
    collectRemoteRects(
        runtimeId: string,
        token: string,
        bucket: SelectorQueryBucket,
        indices: number[],
        coordinateSpace: QueryRectCoordinateSpace,
    ): Promise<IndexedQueryRect[]>;
    disposeRemoteQuery(runtimeId: string, token: string): void;
}

type SessionEntry = LocalSessionEntry | RemoteSessionEntry;

interface LocalSessionEntry {
    kind: 'local';
    node: Element;
}

interface RemoteSessionEntry {
    kind: 'remote';
    runtimeId: string;
    token: string;
    bucket: SelectorQueryBucket;
    index: number;
}

interface SelectorResolution {
    ids: SessionEntry[];
    visible: SessionEntry[];
    actionable: SessionEntry[];
    truncated: boolean;
    usedImplicitFrameFallback: boolean;
    implicitFrameElementSummary?: string;
    failure?: SelectorQueryFailure;
}

interface StoredQuerySession {
    ids: SessionEntry[];
    visible: SessionEntry[];
    actionable: SessionEntry[];
    remoteSessions: RemoteSessionRef[];
    counts: Record<SelectorQueryBucket, number>;
    truncated: boolean;
    usedImplicitFrameFallback: boolean;
    implicitFrameElementSummary?: string;
    failure?: SelectorQueryFailure;
    membershipKey?: string;
}

interface RemoteSessionRef {
    runtimeId: string;
    token: string;
}

const FAILURE_ATTEMPT_LIMIT = 8;

function dedupeElements<T extends Element | Document | ShadowRoot>(values: T[]): T[] {
    const seen = new Set<T>();
    const result: T[] = [];
    for (const value of values) {
        if (seen.has(value)) {
            continue;
        }
        seen.add(value);
        result.push(value);
    }
    return result;
}

function hasNodeType(value: unknown, nodeType: number): boolean {
    return !!value && typeof value === 'object' && 'nodeType' in value && (value as {nodeType?: unknown}).nodeType === nodeType;
}

function isElementNode(value: unknown): value is Element {
    return hasNodeType(value, 1) && typeof (value as Element).tagName === 'string';
}



function shouldStop(results: Element[], limit: number): boolean {
    return limit > 0 && results.length >= limit;
}

function summarizeFrameElement(node: Element): string {
    const parts = [node.tagName.toLowerCase()];
    if (node.id) {
        parts.push(`#${node.id}`);
    }
    const name = node.getAttribute('name');
    if (name) {
        parts.push(`[name="${name}"]`);
    }
    const title = node.getAttribute('title');
    if (title) {
        parts.push(`[title="${title}"]`);
    }
    return parts.join('');
}

function makeSelectorFailure(
    kind: SelectorQueryFailure['kind'],
    summary: string,
    detail?: string,
    frameSummary?: string,
    cause?: BrowserErrorNode,
    attempts?: SelectorQueryAttempt[],
): SelectorQueryFailure {
    const data = attempts && attempts.length > 0 ? {attempts: limitFailureAttempts(attempts)} : undefined;
    return {
        kind,
        summary,
        detail,
        frameSummary,
        error: makeBrowserErrorNode({
            op: 'selector.query',
            kind,
            message: summary,
            detail,
            frameSummary,
            data,
            cause,
        }),
    };
}

function withFailureFrameSummary(failure: SelectorQueryFailure, frameSummary: string): SelectorQueryFailure {
    if (failure.frameSummary) {
        return failure;
    }
    return {
        ...failure,
        frameSummary,
        error: withBrowserErrorFrameSummary(failure.error, frameSummary),
    };
}

function limitFailureAttempts(attempts: SelectorQueryAttempt[] | undefined): SelectorQueryAttempt[] | undefined {
    if (!attempts || attempts.length === 0) {
        return undefined;
    }
    return attempts.slice(0, FAILURE_ATTEMPT_LIMIT).map((attempt) => ({
        selector: attempt.selector,
        kind: attempt.kind,
        summary: attempt.summary,
        detail: attempt.detail,
        frameSummary: attempt.frameSummary,
    }));
}

function attemptFromFailure(selector: string, failure: SelectorQueryFailure): SelectorQueryAttempt {
    return {
        selector,
        kind: failure.kind,
        summary: failure.summary,
        detail: failure.detail,
        frameSummary: failure.frameSummary,
    };
}

function emptyAttempt(selector: string): SelectorQueryAttempt {
    return {
        selector,
        kind: 'empty',
        summary: 'No elements matched this fallback option',
    };
}

function failureWithAttempts(failure: SelectorQueryFailure, attempts: SelectorQueryAttempt[]): SelectorQueryFailure {
    const limited = limitFailureAttempts(attempts);
    if (!failure.error) {
        return failure;
    }
    return {
        ...failure,
        error: makeBrowserErrorNode({
            ...failure.error,
            data: limited && limited.length > 0 ? {
                ...(failure.error.data || {}),
                attempts: limited,
            } : failure.error.data,
        }),
    };
}

function toFrameDocument(node: Element): Document | null {
    if (!isFrameElement(node)) {
        return null;
    }
    try {
        return node.contentDocument;
    } catch {
        return null;
    }
}

export function rectFromElement(node: Element): QueryRect | null {
    const box = visualBoxForElement(node, {includeDescendants: true});
    if (!box) {
        return null;
    }
    return {
        x: box.x,
        y: box.y,
        width: box.width,
        height: box.height,
    };
}

export function projectPointToTopWindow(currentWindow: Window | null, point: QueryPoint): QueryPoint | null {
    let projected = {...point};
    let iter = currentWindow;
    while (iter && iter.parent && iter !== iter.parent) {
        const frameElement = ownerFrameElement(iter);
        if (!frameElement) {
            return null;
        }
        const frameRect = frameElement.getBoundingClientRect();
        projected = {
            x: projected.x + frameRect.left + frameElement.clientLeft,
            y: projected.y + frameRect.top + frameElement.clientTop,
        };
        iter = iter.parent;
    }
    return projected;
}

export function rectFromElementInSpace(node: Element, coordinateSpace: QueryRectCoordinateSpace = 'local'): QueryRect | null {
    const rect = rectFromElement(node);
    if (!rect) {
        return null;
    }
    if (coordinateSpace === 'top') {
        const point = projectPointToTopWindow(node.ownerDocument?.defaultView || null, rect);
        return point ? {...rect, ...point} : null;
    }
    return rect;
}

function bucketCount(session: SelectorQuerySession, bucket: SelectorQueryBucket): number {
    switch (bucket) {
        case 'visible':
            return session.visible.count;
        case 'actionable':
            return session.actionable.count;
        default:
            return session.ids.count;
    }
}

function makeRemoteEntries(session: SelectorQuerySession, bucket: SelectorQueryBucket): RemoteSessionEntry[] {
    const count = bucketCount(session, bucket);
    const entries: RemoteSessionEntry[] = [];
    for (let index = 0; index < count; index++) {
        entries.push({
            kind: 'remote',
            runtimeId: session.runtimeId,
            token: session.token,
            bucket,
            index,
        });
    }
    return entries;
}

function planRequiredLimit(limit: number, plan: SelectorPlan): number {
    for (const layer of plan.layers || []) {
        if (layer.filter?.kind) {
            return 0;
        }
    }
    if (plan.terminal?.kind) {
        return 0;
    }
    return limit;
}

export class SelectorQueryRuntime {
    private readonly sessions = new Map<string, StoredQuerySession>();
    private readonly memberships = new Map<string, Map<string, Set<Element>>>();
    private sessionSeq = 0;

    public constructor(
        private readonly locator: CdpLocator,
        private readonly bridge: SelectorBridge,
    ) {}

    public clear(): void {
        for (const token of Array.from(this.sessions.keys())) {
            this.disposeQuery(token);
        }
        this.sessions.clear();
        this.memberships.clear();
    }

    public async startQuery(
        root: Element | Document | ShadowRoot,
        plan: SelectorPlan,
        options: SelectorQueryOptions = {},
    ): Promise<SelectorQuerySession> {
        const mode: SelectorResolveMode = options.mode || 'load';
        const resultMode: SelectorQueryResultMode = options.resultMode || 'sample';
        const limit = planRequiredLimit(
            typeof options.limit === 'number' && Number.isFinite(options.limit) && options.limit > 0
                ? Math.floor(options.limit)
                : 0,
            plan,
        );
        const actionabilityOptions: ActionabilityOptions = {
            mode: options.actionMode,
            purpose: options.actionPurpose,
            skipStability: true,
            allowOutsideViewport: options.actionPurpose === 'input'
                || options.actionPurpose === 'click'
                || options.actionPurpose === 'hover',
        };
        const resolution = await this.resolvePlan(
            [root],
            plan.layers || [],
            0,
            mode,
            resultMode,
            limit,
            actionabilityOptions,
            options.allowImplicitFrameFallback !== false,
            options.membershipKey,
        );
        this.applyTerminalToResolution(resolution, plan.terminal);
        return this.storeSession(resolution, resultMode, options.membershipKey);
    }

    public validatePlan(root: Element | Document | ShadowRoot, plan: SelectorPlan): void {
        let selectorCount = 0;
        for (const layer of plan.layers || []) {
            for (const option of layer.options || []) {
                if (option.kind !== 'selector' || !option.raw) continue;
                const segments = parseSelectorPath(option.raw);
                if (segments.length === 0) throw new Error('selector is empty');
                selectorCount += segments.length;
                for (const segment of segments) {
                    this.query(root, segment.type, segment.selector, segment.name, segment.exact, 1);
                }
            }
        }
        if (selectorCount === 0) throw new Error('selector plan has no selector');
    }

    public membershipElements(key: string): Element[] {
        const sessions = this.memberships.get(key);
        if (!sessions) return [];
        const result = new Set<Element>();
        for (const elements of sessions.values()) {
            for (const element of elements) {
                if (element.isConnected) result.add(element);
            }
        }
        return Array.from(result);
    }

    public elementInMembership(key: string, element: Element): boolean {
        const sessions = this.memberships.get(key);
        if (!sessions) return false;
        for (const elements of sessions.values()) {
            if (elements.has(element)) return true;
        }
        return false;
    }

    public queryNode(token: string, bucket: SelectorQueryBucket, index: number): Element | null {
        const entry = this.getEntry(token, bucket, index);
        return entry?.kind === 'local' ? entry.node : null;
    }

    public queryNodeInfo(token: string, bucket: SelectorQueryBucket, index: number): SelectorQueryNodeInfo | null {
        const entry = this.getEntry(token, bucket, index);
        if (!entry) {
            return null;
        }
        if (entry.kind === 'local') {
            return {
                kind: 'local',
                runtimeId: this.bridge.runtimeId,
                token,
                bucket,
                index,
            };
        }
        return {
            kind: 'remote',
            runtimeId: entry.runtimeId,
            token: entry.token,
            bucket: entry.bucket,
            index: entry.index,
        };
    }

    public async collectRects(
        token: string,
        bucket: SelectorQueryBucket,
        coordinateSpace: QueryRectCoordinateSpace = 'top',
        limit = 0,
    ): Promise<QueryRect[]> {
        const session = this.sessions.get(token);
        if (!session) {
            return [];
        }
        const entries = this.getBucket(session, bucket);
        const capped = limit > 0 ? entries.slice(0, limit) : entries.slice();
        const rects: QueryRect[] = [];
        const remoteGroups = new Map<string, {runtimeId: string; token: string; bucket: SelectorQueryBucket; indices: number[]}>();
        for (const entry of capped) {
            if (entry.kind === 'local') {
                const rect = rectFromElementInSpace(entry.node, coordinateSpace);
                if (rect) {
                    rects.push(rect);
                }
                continue;
            }
            const key = `${entry.runtimeId}:${entry.token}:${entry.bucket}`;
            const group = remoteGroups.get(key) || {
                runtimeId: entry.runtimeId,
                token: entry.token,
                bucket: entry.bucket,
                indices: [],
            };
            group.indices.push(entry.index);
            remoteGroups.set(key, group);
        }
        for (const group of remoteGroups.values()) {
            const remoteRects = await this.bridge.collectRemoteRects(
                group.runtimeId,
                group.token,
                group.bucket,
                group.indices,
                coordinateSpace,
            );
            rects.push(...remoteRects.map((item) => item.rect));
        }
        return rects;
    }

    public async collectIndexedRects(
        token: string,
        bucket: SelectorQueryBucket,
        indices: number[],
        coordinateSpace: QueryRectCoordinateSpace = 'top',
    ): Promise<IndexedQueryRect[]> {
        const session = this.sessions.get(token);
        if (!session) {
            return [];
        }
        const entries = this.getBucket(session, bucket);
        const wanted = indices.length > 0
            ? indices.filter((index) => Number.isInteger(index) && index >= 0)
            : entries.map((_entry, index) => index);
        const rects: IndexedQueryRect[] = [];
        const remoteGroups = new Map<string, {runtimeId: string; token: string; bucket: SelectorQueryBucket; pairs: Array<{parentIndex: number; remoteIndex: number}>}>();
        for (const index of wanted) {
            const entry = entries[index];
            if (!entry) {
                continue;
            }
            if (entry.kind === 'local') {
                const rect = rectFromElementInSpace(entry.node, coordinateSpace);
                if (rect) {
                    rects.push({index, rect});
                }
                continue;
            }
            const key = `${entry.runtimeId}:${entry.token}:${entry.bucket}`;
            const group = remoteGroups.get(key) || {
                runtimeId: entry.runtimeId,
                token: entry.token,
                bucket: entry.bucket,
                pairs: [],
            };
            group.pairs.push({parentIndex: index, remoteIndex: entry.index});
            remoteGroups.set(key, group);
        }
        for (const group of remoteGroups.values()) {
            const remoteIndices = group.pairs.map((pair) => pair.remoteIndex);
            const remoteRects = await this.bridge.collectRemoteRects(
                group.runtimeId,
                group.token,
                group.bucket,
                remoteIndices,
                coordinateSpace,
            );
            const byRemoteIndex = new Map(remoteRects.map((item) => [item.index, item.rect] as const));
            for (const pair of group.pairs) {
                const rect = byRemoteIndex.get(pair.remoteIndex);
                if (rect) {
                    rects.push({index: pair.parentIndex, rect});
                }
            }
        }
        return rects.sort((left, right) => left.index - right.index);
    }

    public disposeQuery(token?: string): void {
        if (typeof token === 'string' && token.length > 0) {
            const session = this.sessions.get(token);
            this.sessions.delete(token);
            if (session) {
                this.removeMembership(token, session.membershipKey);
                this.disposeRemoteSessions(session.remoteSessions);
            }
            return;
        }
        for (const session of this.sessions.values()) {
            this.disposeRemoteSessions(session.remoteSessions);
        }
        this.sessions.clear();
        this.memberships.clear();
    }

    private getEntry(token: string, bucket: SelectorQueryBucket, index: number): SessionEntry | null {
        if (!Number.isInteger(index) || index < 0) {
            return null;
        }
        const session = this.sessions.get(token);
        if (!session) {
            return null;
        }
        return this.getBucket(session, bucket)[index] ?? null;
    }

    private getBucket(session: StoredQuerySession, bucket: SelectorQueryBucket): SessionEntry[] {
        switch (bucket) {
            case 'visible':
                return session.visible;
            case 'actionable':
                return session.actionable;
            default:
                return session.ids;
        }
    }

    private storeSession(resolution: SelectorResolution, resultMode: SelectorQueryResultMode, membershipKey?: string): SelectorQuerySession {
        const token = `query_session_${Date.now().toString(36)}_${(this.sessionSeq++).toString(36)}`;
        const counts = {
            ids: resolution.ids.length,
            visible: resolution.visible.length,
            actionable: resolution.actionable.length,
        };
        const stored: StoredQuerySession = {
            ids: resultMode === 'count' ? [] : resolution.ids.slice(),
            visible: resultMode === 'count' ? [] : resolution.visible.slice(),
            actionable: resultMode === 'count' ? [] : resolution.actionable.slice(),
            remoteSessions: resultMode === 'count' ? [] : this.collectRemoteSessionRefs(resolution),
            counts,
            truncated: resolution.truncated,
            usedImplicitFrameFallback: resolution.usedImplicitFrameFallback,
            implicitFrameElementSummary: resolution.implicitFrameElementSummary,
            failure: resolution.failure,
            membershipKey,
        };
        this.sessions.set(token, stored);
        if (membershipKey && resultMode !== 'count') {
            let membership = this.memberships.get(membershipKey);
            if (!membership) {
                membership = new Map<string, Set<Element>>();
                this.memberships.set(membershipKey, membership);
            }
            membership.set(token, new Set(
                resolution.ids
                    .filter((entry): entry is LocalSessionEntry => entry.kind === 'local')
                    .map((entry) => entry.node),
            ));
        }
        return {
            token,
            runtimeId: this.bridge.runtimeId,
            ids: {count: counts.ids},
            visible: {count: counts.visible},
            actionable: {count: counts.actionable},
            truncated: resolution.truncated,
            usedImplicitFrameFallback: resolution.usedImplicitFrameFallback,
            implicitFrameElementSummary: resolution.implicitFrameElementSummary,
            failure: resolution.failure,
        };
    }

    private removeMembership(token: string, key?: string): void {
        if (!key) return;
        const membership = this.memberships.get(key);
        membership?.delete(token);
        if (membership?.size === 0) this.memberships.delete(key);
    }

    private collectRemoteSessionRefs(resolution: SelectorResolution): RemoteSessionRef[] {
        const refs = new Map<string, RemoteSessionRef>();
        const collect = (entries: SessionEntry[]) => {
            for (const entry of entries) {
                if (entry.kind !== 'remote') {
                    continue;
                }
                const key = `${entry.runtimeId}:${entry.token}`;
                if (!refs.has(key)) {
                    refs.set(key, {runtimeId: entry.runtimeId, token: entry.token});
                }
            }
        };
        collect(resolution.ids);
        collect(resolution.visible);
        collect(resolution.actionable);
        return Array.from(refs.values());
    }

    private disposeRemoteSessions(refs: RemoteSessionRef[]): void {
        for (const ref of refs) {
            try {
                this.bridge.disposeRemoteQuery(ref.runtimeId, ref.token);
            } catch {
            }
        }
    }

    private async resolvePlan(
        currentRoots: Array<Element | Document | ShadowRoot>,
        layers: SelectorPlanLayer[],
        layerIndex: number,
        mode: SelectorResolveMode,
        resultMode: SelectorQueryResultMode,
        limit: number,
        actionabilityOptions: ActionabilityOptions,
        allowImplicitFrameFallback: boolean,
        membershipKey?: string,
    ): Promise<SelectorResolution> {
        if (layerIndex >= layers.length) {
            return this.buildResolutionFromRoots(currentRoots, mode, actionabilityOptions);
        }
        const layer = layers[layerIndex];
        if (!layer) {
            return this.emptyResolution();
        }
        if (layer.filter?.kind) {
            const filteredRoots = this.applyTerminalToRoots(currentRoots, layer.filter);
            if (filteredRoots.length === 0) {
                return this.emptyResolution();
            }
            return this.resolvePlan(filteredRoots, layers, layerIndex+1, mode, resultMode, limit, actionabilityOptions, allowImplicitFrameFallback, membershipKey);
        }
        if (!Array.isArray(layer.options) || layer.options.length === 0) {
            return this.emptyResolution();
        }
        if (layer.options.every((option) => option.kind === 'frame_enter')) {
            return this.resolveExplicitFrameLayer(currentRoots, layers, layerIndex+1, mode, resultMode, limit, actionabilityOptions, allowImplicitFrameFallback, membershipKey);
        }

        let lastFailure: SelectorQueryFailure | undefined;
        let rejectedMatch: SelectorResolution | undefined;
        const attempts: SelectorQueryAttempt[] = [];
        for (const option of layer.options) {
            if (option.kind !== 'selector' || !option.raw) {
                continue;
            }
            const match = await this.resolveSelectorChain(
                currentRoots,
                option.raw,
                layers.slice(layerIndex+1),
                mode,
                resultMode,
                limit,
                actionabilityOptions,
                allowImplicitFrameFallback,
                membershipKey,
            );
            if (match.ids.length > 0 && this.shouldAcceptFallback(match, mode, actionabilityOptions)) {
                return match;
            }
            if (match.ids.length > 0 && !rejectedMatch) {
                rejectedMatch = match;
            }
            attempts.push(match.failure ? attemptFromFailure(option.raw, match.failure) : this.attemptFromResolution(option.raw, match, mode, actionabilityOptions));
            if (match.failure && !lastFailure) {
                lastFailure = match.failure;
            }
        }
        if (rejectedMatch) {
            rejectedMatch.failure = failureWithAttempts(
                makeSelectorFailure(
                    'no_match',
                    'Selector matched elements, but none satisfied the requested actionability',
                ),
                attempts,
            );
            return rejectedMatch;
        }
        const fallback = this.emptyResolution();
        if (lastFailure) {
            fallback.failure = failureWithAttempts(lastFailure, attempts);
        } else if (attempts.length > 1) {
            fallback.failure = makeSelectorFailure(
                'no_match',
                'All selector fallback options returned no matches',
                undefined,
                undefined,
                undefined,
                attempts,
            );
        }
        return fallback;
    }

    private shouldAcceptFallback(
        resolution: SelectorResolution,
        mode: SelectorResolveMode,
        actionabilityOptions: ActionabilityOptions,
    ): boolean {
        if (mode !== 'actionable') {
            return true;
        }
        if (this.requiresActionableFallback(actionabilityOptions)) {
            return resolution.actionable.length > 0;
        }
        return true;
    }

    private attemptFromResolution(
        selector: string,
        resolution: SelectorResolution,
        mode: SelectorResolveMode,
        actionabilityOptions: ActionabilityOptions,
    ): SelectorQueryAttempt {
        if (resolution.ids.length > 0 && mode === 'actionable' && this.requiresActionableFallback(actionabilityOptions)) {
            return {
                selector,
                kind: 'no_match',
                summary: actionabilityOptions.purpose === 'input'
                    ? 'Selector matched elements, but none are input-actionable'
                    : 'Selector matched elements, but none are click-actionable',
            };
        }
        return emptyAttempt(selector);
    }

    private requiresActionableFallback(actionabilityOptions: ActionabilityOptions): boolean {
        return actionabilityOptions.purpose === 'input'
            || actionabilityOptions.purpose === 'click'
            || actionabilityOptions.purpose === 'hover';
    }

    private async resolveSelectorChain(
        currentRoots: Array<Element | Document | ShadowRoot>,
        raw: string,
        remainingLayers: SelectorPlanLayer[],
        mode: SelectorResolveMode,
        resultMode: SelectorQueryResultMode,
        limit: number,
        actionabilityOptions: ActionabilityOptions,
        allowImplicitFrameFallback: boolean,
        membershipKey?: string,
    ): Promise<SelectorResolution> {
        const segments = parseSelectorPath(raw);
        if (segments.length === 0) {
            return this.emptyResolution();
        }
        let roots = currentRoots.slice();
        for (let index = 0; index < segments.length; index++) {
            const segment = segments[index];
            const segmentLimit = resultMode === 'sample'
                ? (index === segments.length-1 && remainingLayers.length === 0 ? limit : 0)
                : 0;
            let matched: Element[];
            try {
                matched = this.collectLocalNodes(roots, segment, segmentLimit);
            } catch (e) {
                const msg = e instanceof Error ? e.message : String(e);
                const res = this.emptyResolution();
                res.failure = makeSelectorFailure('syntax', msg, undefined, undefined, {
                    op: 'selector.segment',
                    kind: 'syntax',
                    message: msg,
                    selector: segment.raw,
                });
                return res;
            }
            if (matched.length > 0) {
                roots = dedupeElements(matched.slice());
                continue;
            }
            const frameRoots = roots.filter(
                (root): root is HTMLIFrameElement | HTMLFrameElement => isElementNode(root) && isFrameElement(root),
            );
            if (allowImplicitFrameFallback && frameRoots.length > 0) {
                const remainingRaw = segments.slice(index).map((part) => part.raw).join(' >> ');
                const fallbackLayers: SelectorPlanLayer[] = [{
                    options: [{kind: 'selector', raw: remainingRaw}],
                }, ...remainingLayers];
                const fallback = await this.resolveAcrossFrames(frameRoots, {layers: fallbackLayers}, mode, resultMode, limit, true, actionabilityOptions, allowImplicitFrameFallback, membershipKey);
                if (fallback.ids.length > 0) {
                    return fallback;
                }
            }
            return this.emptyResolution();
        }
        if (remainingLayers.length > 0) {
            return this.resolvePlan(roots, remainingLayers, 0, mode, resultMode, limit, actionabilityOptions, allowImplicitFrameFallback, membershipKey);
        }
        return this.buildResolutionFromRoots(roots, mode, actionabilityOptions);
    }

    private collectLocalNodes(
        roots: Array<Element | Document | ShadowRoot>,
        segment: ParsedSelectorSegment,
        limit: number,
    ): Element[] {
        const nodes: Element[] = [];
        const seen = new Set<Element>();
        for (const root of roots) {
            const matched = this.query(root, segment.type, segment.selector, segment.name, segment.exact, limit);
            for (const node of matched) {
                if (seen.has(node)) {
                    continue;
                }
                seen.add(node);
                nodes.push(node);
                if (shouldStop(nodes, limit)) {
                    return nodes;
                }
            }
        }
        return nodes;
    }

    private async resolveExplicitFrameLayer(
        roots: Array<Element | Document | ShadowRoot>,
        layers: SelectorPlanLayer[],
        nextLayerIndex: number,
        mode: SelectorResolveMode,
        resultMode: SelectorQueryResultMode,
        limit: number,
        actionabilityOptions: ActionabilityOptions,
        allowImplicitFrameFallback: boolean,
        membershipKey?: string,
    ): Promise<SelectorResolution> {
        const frameElements = roots.filter(
            (root): root is HTMLIFrameElement | HTMLFrameElement => isElementNode(root) && isFrameElement(root),
        );
        if (frameElements.length === 0) {
            return this.emptyResolution();
        }
        const remainingPlan: SelectorPlan = {
            layers: layers.slice(nextLayerIndex),
        };
        return this.resolveAcrossFrames(frameElements, remainingPlan, mode, resultMode, limit, false, actionabilityOptions, allowImplicitFrameFallback, membershipKey);
    }

    private async resolveAcrossFrames(
        frameElements: Array<HTMLIFrameElement | HTMLFrameElement>,
        plan: SelectorPlan,
        mode: SelectorResolveMode,
        resultMode: SelectorQueryResultMode,
        limit: number,
        implicitFallback: boolean,
        actionabilityOptions: ActionabilityOptions,
        allowImplicitFrameFallback: boolean,
        membershipKey?: string,
    ): Promise<SelectorResolution> {
        const merged = this.emptyResolution();
        for (const frameElement of frameElements) {
            const frameSummary = summarizeFrameElement(frameElement);
            const frameDoc = toFrameDocument(frameElement);
            const remote = await this.bridge.queryChildFrame(frameElement, plan, {
                mode,
                resultMode,
                limit,
                actionMode: actionabilityOptions.mode,
                actionPurpose: actionabilityOptions.purpose,
                allowImplicitFrameFallback,
                membershipKey,
            });
            if (remote.session && remote.session.ids.count > 0) {
                this.mergeResolution(merged, {
                    ids: makeRemoteEntries(remote.session, 'ids'),
                    visible: makeRemoteEntries(remote.session, 'visible'),
                    actionable: makeRemoteEntries(remote.session, 'actionable'),
                    truncated: remote.session.truncated,
                    usedImplicitFrameFallback: !!remote.session.usedImplicitFrameFallback || implicitFallback,
                    implicitFrameElementSummary: remote.session.implicitFrameElementSummary || frameSummary,
                    failure: remote.session.failure,
                });
                if (limit > 0 && merged.ids.length >= limit) {
                    break;
                }
                continue;
            }

            if (frameDoc) {
                const local = await this.resolvePlan([frameDoc], plan.layers || [], 0, mode, resultMode, limit, actionabilityOptions, allowImplicitFrameFallback, membershipKey);
                this.mergeResolution(merged, local);
                if (!merged.implicitFrameElementSummary) {
                    merged.implicitFrameElementSummary = frameSummary;
                }
                if (limit > 0 && merged.ids.length >= limit) {
                    break;
                }
                continue;
            }

            if (remote.failure && !merged.failure) {
                merged.failure = withFailureFrameSummary(remote.failure, frameSummary);
            } else if (remote.session?.failure && !merged.failure) {
                merged.failure = withFailureFrameSummary(remote.session.failure, frameSummary);
            }
        }
        if (implicitFallback && merged.ids.length > 0) {
            merged.usedImplicitFrameFallback = true;
        }
        return merged;
    }

    private async buildResolutionFromRoots(
        roots: Array<Element | Document | ShadowRoot>,
        mode: SelectorResolveMode,
        actionabilityOptions: ActionabilityOptions,
    ): Promise<SelectorResolution> {
        const nodes = roots.filter((root): root is Element => isElementNode(root));
        return this.buildResolutionFromNodes(nodes, mode, actionabilityOptions);
    }

    private async buildResolutionFromNodes(nodes: Element[], mode: SelectorResolveMode, actionabilityOptions: ActionabilityOptions): Promise<SelectorResolution> {
        const ids = nodes.map((node) => ({kind: 'local', node}) satisfies LocalSessionEntry);
        const visibleNodes = nodes.filter((node) => this.locator.isVisible(node));
        const visible = visibleNodes.map((node) => ({kind: 'local', node}) satisfies LocalSessionEntry);
        const actionable: LocalSessionEntry[] = [];
        if (mode === 'actionable') {
            for (const node of nodes) {
                const result = await this.locator.checkActionability(node, actionabilityOptions);
                if (result.actionable || this.acceptsRetriableActionability(result, actionabilityOptions)) {
                    actionable.push({kind: 'local', node});
                }
            }
        }
        return {
            ids,
            visible,
            actionable,
            truncated: false,
            usedImplicitFrameFallback: false,
            failure: undefined,
        };
    }

    private acceptsRetriableActionability(result: ActionabilityDiagnostic, actionabilityOptions: ActionabilityOptions): boolean {
        return !!result.retriable
            && result.retryAction === 'hover_priming'
            && (actionabilityOptions.purpose === 'click' || actionabilityOptions.purpose === 'hover');
    }

    private mergeResolution(target: SelectorResolution, source: SelectorResolution): void {
        target.ids.push(...source.ids);
        target.visible.push(...source.visible);
        target.actionable.push(...source.actionable);
        target.truncated = target.truncated || source.truncated;
        target.usedImplicitFrameFallback = target.usedImplicitFrameFallback || source.usedImplicitFrameFallback;
        target.implicitFrameElementSummary ||= source.implicitFrameElementSummary;
        target.failure ||= source.failure;
    }

    private emptyResolution(): SelectorResolution {
        return {
            ids: [],
            visible: [],
            actionable: [],
            truncated: false,
            usedImplicitFrameFallback: false,
            failure: undefined,
        };
    }

    private applyTerminalToResolution(resolution: SelectorResolution, terminal: SelectorPlanTerminal | undefined): void {
        if (!terminal?.kind) {
            return;
        }
        resolution.ids = this.applyTerminalToEntries(resolution.ids, terminal);
        resolution.visible = this.applyTerminalToEntries(resolution.visible, terminal);
        resolution.actionable = this.applyTerminalToEntries(resolution.actionable, terminal);
    }

    private applyTerminalToEntries(entries: SessionEntry[], terminal: SelectorPlanTerminal): SessionEntry[] {
        if (entries.length === 0) {
            return [];
        }
        switch (terminal.kind) {
            case 'last':
                return [entries[entries.length - 1]];
            case 'nth': {
                const index = Number.isFinite(terminal.index) ? Math.floor(terminal.index as number) : 0;
                if (index < 0 || index >= entries.length) {
                    return [];
                }
                return [entries[index]];
            }
            default:
                return entries.slice();
        }
    }

    private applyTerminalToRoots(
        roots: Array<Element | Document | ShadowRoot>,
        terminal: SelectorPlanTerminal,
    ): Array<Element | Document | ShadowRoot> {
        if (roots.length === 0) {
            return [];
        }
        switch (terminal.kind) {
            case 'last':
                return [roots[roots.length - 1]];
            case 'nth': {
                const index = Number.isFinite(terminal.index) ? Math.floor(terminal.index as number) : 0;
                if (index < 0 || index >= roots.length) {
                    return [];
                }
                return [roots[index]];
            }
            default:
                return roots.slice();
        }
    }

    private query(
        root: Element | Document | ShadowRoot,
        type: string,
        selector: string,
        name?: string,
        exact?: boolean,
        limit = 0,
    ): Element[] {
        if (!type || type === 'css') {
            return this.querySelectorAllWithShadow(root, selector, limit, false);
        }
        if (type === 'xpath') {
            return this.querySelectorAllWithShadow(root, selector, limit, true);
        }
        switch (type) {
            case 'role':
                return getByRole(root, selector, name, limit);
            case 'text':
                return getByText(root, selector, !!exact, limit);
            case 'has-text':
                return getByHasText(root, selector, !!exact, limit);
            case 'placeholder':
                return getByPlaceholder(root, selector, !!exact, limit);
            case 'label':
                return getByLabel(root, selector, !!exact, limit);
            case 'testid':
                return getByTestId(root, selector, limit);
            default:
                return [];
        }
    }

    private querySelectorAllWithShadow(
        root: Element | Document | ShadowRoot,
        selector: string,
        limit: number,
        forceXPath: boolean,
    ): Element[] {
        const results: Element[] = [];
        const seen = new Set<Element>();
        const stop = () => limit > 0 && results.length >= limit;
        const useXPath = forceXPath
            || selector.startsWith('/')
            || selector.startsWith('(')
            || selector.startsWith('./')
            || selector.startsWith('xpath/')
            || selector.startsWith('xpath=');
        const documentAbsoluteXPath = useXPath && isDocumentAbsoluteXPath(selector);

        if (!useXPath && isElementNode(root)) {
            const trimmed = selector.trimStart();
            if (trimmed.startsWith('+')) {
                const sub = trimmed.slice(1).trimStart();
                const next = root.nextElementSibling;
                if (next && next.matches(sub)) {
                    results.push(next);
                }
                return results;
            }
            if (trimmed.startsWith('~')) {
                const sub = trimmed.slice(1).trimStart();
                let sibling = root.nextElementSibling;
                while (sibling && !stop()) {
                    if (sibling.matches(sub) && !seen.has(sibling)) {
                        seen.add(sibling);
                        results.push(sibling);
                    }
                    sibling = sibling.nextElementSibling;
                }
                return results;
            }
        }

        const cssSelector = !useXPath && /^\s*>/.test(selector)
            ? ':scope ' + selector.trimStart()
            : selector;

        let firstError: Error | null = null;
        const visit = (nodeRoot: Element | Document | ShadowRoot): boolean => {
            if (stop()) {
                return true;
            }
            if (isElementNode(nodeRoot) && isInternalElement(nodeRoot)) {
                return false;
            }
            try {
                const matches = useXPath
                    ? evaluateXPathElements(nodeRoot, selector)
                    : nodeRoot.querySelectorAll(cssSelector);
                for (const node of matches) {
                    if (seen.has(node) || closestInternalElement(node)) {
                        continue;
                    }
                    seen.add(node);
                    results.push(node);
                    if (stop()) {
                        return true;
                    }
                }
            } catch (e) {
                firstError ||= e instanceof Error ? e : new Error(String(e));
            }
            if (documentAbsoluteXPath) {
                return stop();
            }
            const elements = nodeRoot.querySelectorAll('*');
            for (const element of elements) {
                const shadow = shadowRootFor(element);
                if (!isInternalElement(element) && shadow && visit(shadow)) {
                    return true;
                }
            }
            return stop();
        };
        if (!documentAbsoluteXPath && isElementNode(root) && !isInternalElement(root) && shadowRootFor(root)) {
            visit(shadowRootFor(root)!);
        }
        visit(root);
        if (results.length === 0 && firstError) {
            throw firstError;
        }
        return results;
    }
}
