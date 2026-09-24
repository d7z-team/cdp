type RuntimeSlot = 'ffi';

declare global {
    interface Window {
        __cdp_ffi?: unknown;
    }
}

function getRuntimeSlot<T>(slot: RuntimeSlot, win: Window | null = window): T | undefined {
    if (!win) return undefined;
    switch (slot) {
        case 'ffi': return win.__cdp_ffi as T | undefined;
    }
}

function setRuntimeSlot<T>(slot: RuntimeSlot, value: T, win: Window = window): void {
    const name = runtimeSlotPropertyName(slot);
    Object.defineProperty(win, name, {
        value,
        configurable: true,
        writable: true,
        enumerable: false,
    });
}

function runtimeSlotPropertyName(slot: RuntimeSlot): '__cdp_ffi' {
    switch (slot) {
        case 'ffi':
            return '__cdp_ffi';
    }
}

function clearRuntimeSlot<T>(slot: RuntimeSlot, expected?: T, win: Window = window): void {
    if (expected !== undefined && getRuntimeSlot(slot, win) !== expected) {
        return;
    }
    delete win[runtimeSlotPropertyName(slot)];
}

export function getCdpFFI<T = unknown>(win: Window | null = window): T | undefined {
    return getRuntimeSlot<T>('ffi', win);
}

export function setCdpFFI<T>(value: T, win: Window = window): void {
    setRuntimeSlot('ffi', value, win);
}

export function clearCdpFFI<T>(expected?: T, win: Window = window): void {
    clearRuntimeSlot('ffi', expected, win);
}
