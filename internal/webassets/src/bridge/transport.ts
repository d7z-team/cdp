import {registeredFrameIdentity} from "./frame_identity";
import {xchacha20poly1305} from '@noble/ciphers/chacha.js';
import {bytesToHex, hexToBytes} from '@noble/ciphers/utils.js';
import type {WireMessage} from './types';

// Only isolated-world scripts receive these keys. Nothing secret crosses postMessage.
// A fresh challenge binds every delivery to the current recipient document, so a
// captured packet cannot be delivered to a replacement frame or replayed later.
type Envelope = {
    kind: 'hello' | 'welcome' | 'message';
    source: string;
    target: string;
    runtime: string;
    recipient?: string;
    challenge: string;
    message?: WireMessage;
    grant?: string;
};

type Delivery = {
    target: Window;
    message: WireMessage;
    resolve(): void;
    reject(error: Error): void;
    timer: number;
    prepare?: (runtimeId: string) => boolean;
};

const MAX_PACKET_BYTES = 16 * 1024 * 1024;
const HANDSHAKE_TIMEOUT = 1500;
const encoder = new TextEncoder();
const decoder = new TextDecoder();

export class FrameTransport {
    private readonly keys = new Map<string, Uint8Array>();
    private readonly deliveries = new Map<string, Delivery>();
    private readonly grants = new Map<string, {source: Window; runtime: string; timer: number}>();
    private destroyed = false;

    constructor(readonly runtimeId: string, key: string) {
        this.addKey(key);
    }

    addKey(key: string): void {
        if (!this.destroyed && /^[a-f0-9]{64}$/.test(key) && !this.keys.has(key)) {
            this.keys.set(key, hexToBytes(key));
        }
    }

    send(target: Window, message: WireMessage, prepare?: (runtimeId: string) => boolean): Promise<void> {
        if (this.destroyed) return Promise.reject(new Error('Frame transport closed'));
        const challenge = bytesToHex(crypto.getRandomValues(new Uint8Array(24)));
        return new Promise((resolve, reject) => {
            const timer = window.setTimeout(() => {
                this.deliveries.delete(challenge);
                reject(new Error('Frame transport handshake timed out'));
            }, HANDSHAKE_TIMEOUT);
            this.deliveries.set(challenge, {target, message, resolve, reject, timer, prepare});
            try {
                this.post(target, {kind: 'hello', source: windowPath(window), target: windowPath(target),
                    runtime: this.runtimeId, challenge});
            } catch (error) {
                window.clearTimeout(timer);
                this.deliveries.delete(challenge);
                reject(error);
            }
        });
    }

    receive(event: MessageEvent<unknown>): WireMessage | undefined {
        if (this.destroyed || !event.isTrusted || !event.source || event.source === window
            || !(event.data instanceof Uint8Array) || event.data.length < 40
            || event.data.length > MAX_PACKET_BYTES) return;
        const source = event.source as Window;
        let packet: Envelope | undefined;
        for (const key of this.keys.values()) {
            try {
                packet = JSON.parse(decoder.decode(xchacha20poly1305(key, event.data.subarray(0, 24))
                    .decrypt(event.data.subarray(24)))) as Envelope;
                break;
            } catch { /* Unauthenticated input is ignored without a response. */ }
        }
        if (!packet || typeof packet.runtime !== 'string' || !/^[a-f0-9]{48}$/.test(packet.challenge)) return;
        try {
            if (packet.target !== windowPath(window) || packet.source !== windowPath(source)) return;
        } catch { return; }

        if (packet.kind === 'hello') {
            // Duplicate hello packets are possible when attaching managers share a runtime.
            // Reissuing a grant is harmless: its receiver-generated challenge is one-use.
            if (this.grants.size >= 1024) return;
            const grant = bytesToHex(crypto.getRandomValues(new Uint8Array(24)));
            const timer = window.setTimeout(() => this.grants.delete(grant), HANDSHAKE_TIMEOUT);
            this.grants.set(grant, {source, runtime: packet.runtime, timer});
            this.post(source, {kind: 'welcome', source: packet.target, target: packet.source,
                runtime: this.runtimeId, recipient: packet.runtime, challenge: packet.challenge,
                grant});
            return;
        }
        if (packet.kind === 'welcome') {
            const delivery = this.deliveries.get(packet.challenge);
            const grant = packet.grant;
            if (!delivery || delivery.target !== source || packet.recipient !== this.runtimeId
                || typeof grant !== 'string' || !/^[a-f0-9]{48}$/.test(grant)) return;
            this.deliveries.delete(packet.challenge);
            window.clearTimeout(delivery.timer);
            try {
                if (delivery.prepare && !delivery.prepare(packet.runtime)) {
                    delivery.resolve();
                    return;
                }
                this.post(source, {kind: 'message', source: packet.target, target: packet.source,
                    runtime: this.runtimeId, recipient: packet.runtime, challenge: grant,
                    message: delivery.message});
                delivery.resolve();
            } catch (error) {
                delivery.reject(error instanceof Error ? error : new Error('Frame delivery failed'));
            }
            return;
        }
        if (packet.kind !== 'message' || packet.recipient !== this.runtimeId) return;
        const grant = this.grants.get(packet.challenge);
        if (!grant || grant.source !== source || grant.runtime !== packet.runtime) return;
        this.grants.delete(packet.challenge);
        window.clearTimeout(grant.timer);
        const message = packet.message;
        if (!message || typeof message.channel !== 'string' || typeof message.srcRuntimeId !== 'string'
            || !['request', 'response', 'event'].includes(message.direction)) return;
        return message;
    }

    destroy(): void {
        this.destroyed = true;
        for (const delivery of this.deliveries.values()) {
            window.clearTimeout(delivery.timer);
            delivery.reject(new Error('Frame transport closed'));
        }
        for (const grant of this.grants.values()) window.clearTimeout(grant.timer);
        this.deliveries.clear();
        this.grants.clear();
        for (const key of this.keys.values()) key.fill(0);
        this.keys.clear();
    }

    private post(target: Window, packet: Envelope): void {
        const plaintext = encoder.encode(JSON.stringify(packet));
        if (plaintext.length + 40 > MAX_PACKET_BYTES) throw new Error('Frame packet exceeds size limit');
        for (const key of this.keys.values()) {
            const nonce = crypto.getRandomValues(new Uint8Array(24));
            const ciphertext = xchacha20poly1305(key, nonce).encrypt(plaintext);
            const bytes = new Uint8Array(24 + ciphertext.length);
            bytes.set(nonce);
            bytes.set(ciphertext, 24);
            target.postMessage(bytes, '*');
        }
    }
}

// Window hierarchy access is available across origins; DOM access is not needed.
function windowPath(target: Window): string {
    const identity = registeredFrameIdentity(target);
    if (identity) return identity;
    const path: number[] = [];
    for (let depth = 0; target !== target.top; depth++) {
        if (depth >= 64) throw new Error('Frame nesting exceeds transport limit');
        const parent = target.parent;
        let index = 0;
        while (index < parent.length && parent[index] !== target) index++;
        if (index === parent.length) throw new Error('Frame detached');
        path.push(index);
        target = parent;
    }
    return path.reverse().join('/');
}
