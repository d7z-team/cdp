import {authorShadowRoots} from "../dom/shadow";
export class DOMRevision {
    private value = 0;
    private observer: MutationObserver | null = null;
    private listeners = new Set<() => void>();

    start(): void {
        if (!document.documentElement) return;
        const options: MutationObserverInit = {subtree: true, childList: true, attributes: true, characterData: true};
        if (!this.observer) {
            this.observer = new MutationObserver(() => {
                this.value++;
                for (const listener of this.listeners) listener();
            });
            this.observer.observe(document.documentElement, options);
        }
        for (const root of authorShadowRoots()) this.observer.observe(root, options);
    }

    current(): number {
        return this.value;
    }

    waitForQuiet(quietMs: number, timeoutMs: number): Promise<{revision: number; timed_out: boolean}> {
        this.start();
        quietMs = Math.max(0, quietMs);
        timeoutMs = Math.max(quietMs, timeoutMs);
        return new Promise(resolve => {
            let quietTimer = 0;
            let timeoutTimer = 0;
            const finish = (timedOut: boolean) => {
                window.clearTimeout(quietTimer);
                window.clearTimeout(timeoutTimer);
                this.listeners.delete(changed);
                resolve({revision: this.value, timed_out: timedOut});
            };
            const changed = () => {
                window.clearTimeout(quietTimer);
                quietTimer = window.setTimeout(() => finish(false), quietMs);
            };
            this.listeners.add(changed);
            quietTimer = window.setTimeout(() => finish(false), quietMs);
            timeoutTimer = window.setTimeout(() => finish(true), timeoutMs);
        });
    }

    destroy(): void {
        this.observer?.disconnect();
        this.observer = null;
        this.listeners.clear();
    }
}
