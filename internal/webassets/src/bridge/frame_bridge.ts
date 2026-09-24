import {frameElements} from "../dom/shadow";
import {
    WireMessage, PostOptions, Unsubscribe,
    BRIDGE_MESSAGE_TYPE, DEFAULT_REQUEST_TIMEOUT_MS, Channel,
} from "./types";
import { isFrameElement } from "../utils/frame_element";
import { getCdpFFI } from "../runtime/globals";

import {FrameTransport} from "./transport";

type FrameElement = HTMLIFrameElement | HTMLFrameElement;

interface PendingRequest {
    resolve: (result: unknown) => void;
    reject: (error: Error) => void;
    timer: number;
    destinationRuntimeId?: string;
    target: Window;
    channel: string;
}

interface ResponseEnvelope {
    ok: boolean;
    value?: unknown;
    error?: string;
}

export class FrameBridge {
    readonly runtimeId: string;

    private readonly messageHandler: (event: MessageEvent<unknown>) => void;
    private readonly messageListeners = new Map<string, Set<(payload: unknown, src: MessageEvent) => void>>();
    private readonly requestHandlers = new Map<string, (payload: unknown, src: MessageEvent) => unknown | Promise<unknown>>();
    private readonly pendingRequests = new Map<string, PendingRequest>();
    private readonly runtimeWindows = new Map<string, Window>();
    private readonly runtimeIdsByWindow = new WeakMap<Window, string>();
    private readonly departedRuntimeIds = new Set<string>();
    private destroyed = false;
    private readonly transport: FrameTransport;

    constructor(runtimeId: string) {
        this.transport = new FrameTransport(runtimeId, "__FRAME_TRANSPORT_KEY__");
        this.runtimeId = runtimeId;
        this.runtimeWindows.set(runtimeId, window);
        this.messageHandler = this.handleMessage.bind(this);
        window.addEventListener('message', this.messageHandler);
        window.setTimeout(() => this.announceRuntime(), 0);
    }

    // ── Event (fire-and-forget) ──────────────────────────────────────────

    post<T = unknown>(channel: string, payload: T, opts: PostOptions = {}): void {
        if (this.destroyed) return;
        const target = this.resolveTarget(opts.target ?? 'parent');
        if (!target || target === window) return;
        void this.transport.send(target, this.makeEvent(channel, payload, target)).catch(() => undefined);
    }

    sendTo<T = unknown>(target: Window, channel: string, payload: T): void {
        if (this.destroyed || !target || target === window) return;
        void this.transport.send(target, this.makeEvent(channel, payload, target)).catch(() => undefined);
    }

    broadcast<T = unknown>(channel: string, payload: T): void {
        if (this.destroyed) return;
        const frames = frameElements();
        frames.forEach(frame => {
            if (!isFrameElement(frame)) return;
            const cw = frame.contentWindow;
            if (cw) this.sendTo(cw, channel, payload);
        });
    }

    // ── Request-response ─────────────────────────────────────────────────

    request<TReq = unknown, TRes = unknown>(
        channel: string,
        payload: TReq,
        target: Window,
        timeoutMs: number = DEFAULT_REQUEST_TIMEOUT_MS,
        destinationRuntimeId?: string,
    ): Promise<TRes> {
        if (!target || target === window || this.destroyed) {
            return Promise.reject(new Error('FrameBridge: invalid request target'));
        }
        const requestId = `${channel}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
        const msg: WireMessage = {
            type: BRIDGE_MESSAGE_TYPE,
            direction: 'request',
            channel,
            requestId,
            srcRuntimeId: this.runtimeId,
            dstRuntimeId: destinationRuntimeId || this.runtimeIdsByWindow.get(target),
            payload,
        };
        return new Promise<TRes>((resolve, reject) => {
            const timer = window.setTimeout(() => {
                this.pendingRequests.delete(requestId);
                reject(new Error(`FrameBridge: request ${channel} timed out after ${timeoutMs}ms`));
            }, timeoutMs);
            this.pendingRequests.set(requestId, {
                resolve: resolve as (result: unknown) => void,
                reject,
                timer,
                destinationRuntimeId: msg.dstRuntimeId,
                target, channel,
            });
            void this.transport.send(target, msg, runtimeId => {
                const pending = this.pendingRequests.get(requestId);
                if (!pending) return false;
                if (!destinationRuntimeId) {
                    pending.destinationRuntimeId = runtimeId;
                    msg.dstRuntimeId = runtimeId;
                    this.rememberRuntimeWindow(runtimeId, target, true);
                }
                return true;
            }).catch(error => {
                window.clearTimeout(timer);
                this.pendingRequests.delete(requestId);
                reject(error instanceof Error ? error : new Error('FrameBridge: delivery failed'));
            });
        });
    }

    // ── Listeners ────────────────────────────────────────────────────────

    onMessage<T = unknown>(
        channel: string,
        handler: (payload: T, src: MessageEvent) => void,
    ): Unsubscribe {
        let set = this.messageListeners.get(channel);
        if (!set) {
            set = new Set();
            this.messageListeners.set(channel, set);
        }
        const wrapped = handler as (payload: unknown, src: MessageEvent) => void;
        set.add(wrapped);
        return () => {
            set?.delete(wrapped);
            if (set && set.size === 0) {
                this.messageListeners.delete(channel);
            }
        };
    }

    onRequest<TReq = unknown, TRes = unknown>(
        channel: string,
        handler: (payload: TReq, src: MessageEvent) => TRes | Promise<TRes>,
    ): Unsubscribe {
        this.requestHandlers.set(channel, handler as (payload: unknown, src: MessageEvent) => unknown | Promise<unknown>);
        return () => {
            if (this.requestHandlers.get(channel) === handler) {
                this.requestHandlers.delete(channel);
            }
        };
    }

    // ── Frame lookup utilities ───────────────────────────────────────────

    findWindowByRuntimeId(runtimeId: string): Window | null {
        if (!runtimeId || runtimeId === this.runtimeId) {
            return runtimeId === this.runtimeId ? window : null;
        }
        if (this.departedRuntimeIds.has(runtimeId)) return null;
        const registered = this.runtimeWindows.get(runtimeId);
        if (registered) {
            if (this.isReachableWindow(registered)) {
                return registered;
            }
            this.forgetRuntime(runtimeId, registered);
        }
        const frames = Array.from(frameElements()) as FrameElement[];
        for (const frame of frames) {
            try {
                const candidate = frame.contentWindow;
                const info = getCdpFFI<{ runtimeInfo?: () => { runtimeId?: string } }>(candidate)?.runtimeInfo?.();
                if (info?.runtimeId === runtimeId) {
                    return candidate;
                }
            } catch {
            }
        }
        return null;
    }

    forgetRuntime(runtimeId: string, source?: Window): boolean {
        if (!runtimeId || runtimeId === this.runtimeId) return false;
        const target = this.runtimeWindows.get(runtimeId);
        if (source && target && target !== source) return false;
        this.departedRuntimeIds.add(runtimeId);
        if (target) this.runtimeWindows.delete(runtimeId);
        if (target && this.runtimeIdsByWindow.get(target) === runtimeId) {
            this.runtimeIdsByWindow.delete(target);
        }
        let removed = !!target;
        for (const [requestId, pending] of this.pendingRequests) {
            if (pending.destinationRuntimeId !== runtimeId) continue;
            window.clearTimeout(pending.timer);
            this.pendingRequests.delete(requestId);
            pending.reject(new Error(`FrameBridge: runtime ${runtimeId} is unavailable`));
            removed = true;
        }
        return removed;
    }

    directRuntimeIdForWindow(target: Window): string | undefined {
        return this.runtimeIdsByWindow.get(target);
    }

    requestRuntime<TReq = unknown, TRes = unknown>(
        runtimeId: string,
        channel: string,
        payload: TReq,
        timeoutMs: number = DEFAULT_REQUEST_TIMEOUT_MS,
    ): Promise<TRes> {
        const target = this.findWindowByRuntimeId(runtimeId);
        if (!target || target === window) {
            return Promise.reject(new Error(`FrameBridge: runtime ${runtimeId || '<empty>'} is unavailable`));
        }
        return this.request<TReq, TRes>(channel, payload, target, timeoutMs, runtimeId);
    }

    findFrameElementByWindow(targetWindow: Window): FrameElement | null {
        for (const frame of Array.from(frameElements())) {
            const candidate = frame as FrameElement;
            try {
                if (candidate.contentWindow === targetWindow) {
                    return candidate;
                }
            } catch {
            }
        }
        return null;
    }

    // ── Lifecycle ────────────────────────────────────────────────────────

    addTransportKey(key: string): void {
        this.transport.addKey(key);
    }

    destroy(): void {
        if (this.destroyed) return;
        this.destroyed = true;
        this.transport.destroy();
        window.removeEventListener('message', this.messageHandler);
        for (const pending of this.pendingRequests.values()) {
            window.clearTimeout(pending.timer);
            pending.reject(new Error('FrameBridge destroyed'));
        }
        this.pendingRequests.clear();
        this.messageListeners.clear();
        this.requestHandlers.clear();
        this.runtimeWindows.clear();
        this.departedRuntimeIds.clear();
    }

    // ── Private ──────────────────────────────────────────────────────────

    private resolveTarget(t: Window | 'parent' | 'top'): Window | null {
        if (t === 'parent') return window.parent;
        if (t === 'top') return window.top;
        return t;
    }

    private makeEvent<T>(channel: string, payload: T, target: Window): WireMessage {
        return {
            type: BRIDGE_MESSAGE_TYPE,
            direction: 'event',
            channel,
            srcRuntimeId: this.runtimeId,
            dstRuntimeId: this.runtimeIdsByWindow.get(target),
            payload,
        };
    }

    private handleMessage(event: MessageEvent<unknown>): void {
        if (getCdpFFI<{bridge?: FrameBridge}>()?.bridge !== this) {
            this.destroy();
            return;
        }
        const data = this.transport.receive(event);
        if (!data || data.type !== BRIDGE_MESSAGE_TYPE || !data.channel) return;
        if (data.dstRuntimeId && data.dstRuntimeId !== this.runtimeId) {
            const nextHop = this.runtimeWindows.get(data.dstRuntimeId);
            if (nextHop && nextHop !== event.source && this.isReachableWindow(nextHop)) {
                void this.transport.send(nextHop, data).catch(() => undefined);
            }
            return;
        }

        if (data.direction === 'response') {
            this.handleResponse(data, event);
            return;
        }

        this.rememberRuntimeWindow(
            data.srcRuntimeId,
            event.source,
            data.channel === Channel.RUNTIME_READY,
        );

        if (data.direction === 'request') {
            void this.handleIncomingRequest(data, event);
            return;
        }

        // direction === 'event'
        if (data.channel === Channel.RUNTIME_READY) {
            const payload = data.payload as {runtimeId?: string} | null;
            const announcedRuntimeId = payload?.runtimeId || data.srcRuntimeId;
            if (announcedRuntimeId && announcedRuntimeId !== data.srcRuntimeId && event.source
                && !this.departedRuntimeIds.has(announcedRuntimeId)) {
                this.runtimeWindows.set(announcedRuntimeId, event.source as Window);
            }
            if (window.parent !== window) {
                this.post(Channel.RUNTIME_READY, payload || {runtimeId: data.srcRuntimeId}, {target: 'parent'});
            }
        }
        const set = this.messageListeners.get(data.channel);
        if (set) {
            for (const handler of set) {
                try { handler(data.payload, event); } catch (error) {
                    console.error(`[FrameBridge] event handler error on channel "${data.channel}":`, error);
                }
            }
        }
    }

    private handleResponse(data: WireMessage, event: MessageEvent): void {
        if (!data.requestId) return;
        const pending = this.pendingRequests.get(data.requestId);
        if (!pending || event.source !== pending.target || data.channel !== pending.channel
            || data.dstRuntimeId !== this.runtimeId
            || (pending.destinationRuntimeId && pending.destinationRuntimeId !== data.srcRuntimeId)) return;
        this.rememberRuntimeWindow(data.srcRuntimeId, event.source, false);
        window.clearTimeout(pending.timer);
        this.pendingRequests.delete(data.requestId);
        const payload = data.payload as ResponseEnvelope | unknown;
        if (isResponseEnvelope(payload)) {
            if (payload.ok) {
                pending.resolve(payload.value);
            } else {
                pending.reject(new Error(payload.error || `FrameBridge: request ${data.channel} failed`));
            }
            return;
        }
        pending.resolve(payload);
    }

    private async handleIncomingRequest(data: WireMessage, event: MessageEvent): Promise<void> {
        const handler = this.requestHandlers.get(data.channel);
        const sourceWindow = event.source as Window | null;
        if (!sourceWindow) return;
        if (!handler) {
            this.sendResponse(sourceWindow, data, {ok: false, error: `FrameBridge: no handler for ${data.channel}`});
            return;
        }
        try {
            const result = await handler(data.payload, event);
            this.sendResponse(sourceWindow, data, {ok: true, value: result});
        } catch (error) {
            console.error(`[FrameBridge] request handler error on channel "${data.channel}":`, error);
            this.sendResponse(sourceWindow, data, {
                ok: false,
                error: error instanceof Error ? error.message : String(error ?? 'unknown error'),
            });
        }
    }

    private sendResponse(target: Window, request: WireMessage, payload: ResponseEnvelope): void {
        const response: WireMessage = {
            type: BRIDGE_MESSAGE_TYPE,
            direction: 'response',
            channel: request.channel,
            requestId: request.requestId,
            srcRuntimeId: this.runtimeId,
            dstRuntimeId: request.srcRuntimeId,
            payload,
        };
        void this.transport.send(target, response).catch(() => undefined);
    }

    private announceRuntime(): void {
        if (this.destroyed || window.parent === window) return;
        this.post(Channel.RUNTIME_READY, {runtimeId: this.runtimeId}, {target: 'parent'});
    }

    private rememberRuntimeWindow(
        runtimeId: string | undefined,
        source: MessageEventSource | null,
        replaceDirectRuntime: boolean,
    ): void {
        if (!runtimeId || this.departedRuntimeIds.has(runtimeId) || !source || source === window) return;
        try {
            const sourceWindow = source as Window;
            if (typeof sourceWindow.postMessage !== 'function') return;
            const previousRuntimeId = this.runtimeIdsByWindow.get(sourceWindow);
            if (previousRuntimeId && previousRuntimeId !== runtimeId) {
                if (!replaceDirectRuntime) {
                    this.runtimeWindows.set(runtimeId, sourceWindow);
                    return;
                }
                this.forgetRuntime(previousRuntimeId, sourceWindow);
            }
            this.runtimeIdsByWindow.set(sourceWindow, runtimeId);
            this.runtimeWindows.set(runtimeId, sourceWindow);
        } catch {
        }
    }

    private isReachableWindow(target: Window): boolean {
        try {
            return !target.closed;
        } catch {
            return false;
        }
    }
}

function isResponseEnvelope(value: unknown): value is ResponseEnvelope {
    return !!value
        && typeof value === 'object'
        && typeof (value as ResponseEnvelope).ok === 'boolean';
}
