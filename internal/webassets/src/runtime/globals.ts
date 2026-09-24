declare global {
    interface Window {
        __cdp_ffi?: unknown;
    }
}

export function getCdpFFI<T = unknown>(win: Window | null = window): T | undefined {
    return win?.__cdp_ffi as T | undefined;
}

export function setCdpFFI<T>(value: T, win: Window = window): void {
    Object.defineProperty(win, '__cdp_ffi', {
        value,
        configurable: true,
        writable: true,
        enumerable: false,
    });
}

export function clearCdpFFI<T>(expected?: T, win: Window = window): void {
    if (expected !== undefined && getCdpFFI(win) !== expected) return;
    delete win.__cdp_ffi;
}
