import {
    clearCdpFFI,
    getCdpFFI,
    setCdpFFI,
} from "./globals";

export type RuntimeLifecycleState = 'idle' | 'starting' | 'ready' | 'failed';

export interface RuntimeLifecycleFacade {
    runtimeReady(): boolean;
    setRuntimeReady(): void;
    isRuntimeGeneration(generation: number): boolean;
    destroy(): unknown;
}

type RuntimeSlot = 'ffi';

interface RuntimeLifecycleSlot<T extends RuntimeLifecycleFacade> {
    instance?: T;
    state: RuntimeLifecycleState;
    generation: number;
    completion?: Promise<T | null>;
    resolveCompletion?: (instance: T | null) => void;
    failure?: unknown;
}

declare global {
    interface Window {
        __cdp_runtime_lifecycle?: Partial<Record<RuntimeSlot, RuntimeLifecycleSlot<RuntimeLifecycleFacade>>>;
    }
}

function runtimeLifecycleSlot<T extends RuntimeLifecycleFacade>(slot: RuntimeSlot): RuntimeLifecycleSlot<T> {
    let root = window.__cdp_runtime_lifecycle;
    if (!root) {
        root = {};
        Object.defineProperty(window, '__cdp_runtime_lifecycle', {
            value: root,
            configurable: true,
            writable: true,
            enumerable: false,
        });
    }
    let state = root[slot] as RuntimeLifecycleSlot<T> | undefined;
    if (!state) {
        state = {state: 'idle', generation: 0};
        root[slot] = state as unknown as RuntimeLifecycleSlot<RuntimeLifecycleFacade>;
    }
    return state;
}

function discardRuntimeSlot<T extends RuntimeLifecycleFacade>(
    slotName: RuntimeSlot,
    slot: RuntimeLifecycleSlot<T>,
    instance: T | undefined,
    failure: unknown,
): void {
    if (instance && slot.instance !== instance) return;
    slot.state = 'failed';
    slot.failure = failure;
    slot.resolveCompletion?.(null);
    const root = window.__cdp_runtime_lifecycle;
    const current = root?.[slotName] as RuntimeLifecycleSlot<T> | undefined;
    if (root && current === slot) delete root[slotName];
}

export function markRuntimeDestroyed<T extends RuntimeLifecycleFacade>(slot: RuntimeSlot, instance: T): void {
    const state = window.__cdp_runtime_lifecycle?.[slot] as RuntimeLifecycleSlot<T> | undefined;
    if (!state) return;
    discardRuntimeSlot(slot, state, instance, state.failure || new Error(`[CDP] ${slot} runtime destroyed`));
}

function ensureCompletion<T extends RuntimeLifecycleFacade>(slot: RuntimeLifecycleSlot<T>): Promise<T | null> {
    if (!slot.completion) {
        slot.completion = new Promise<T | null>((resolve) => {
            slot.resolveCompletion = resolve;
        });
    }
    return slot.completion;
}

export async function ensureRuntimeStarted<T extends RuntimeLifecycleFacade>(
    slotName: RuntimeSlot,
    create: (generation: number) => T,
    bootstrap: (instance: T) => Promise<void>,
): Promise<T | null> {
    const slot = runtimeLifecycleSlot<T>(slotName);
    const existing = getCdpFFI<T>();
    if (slot.state === 'ready' && existing?.runtimeReady?.()) {
        slot.instance = existing;
        return existing;
    }
    if (slot.state === 'starting') {
        return ensureCompletion(slot);
    }
    slot.completion = undefined;
    slot.resolveCompletion = undefined;
    const completion = ensureCompletion(slot);
    const generation = slot.generation + 1;
    slot.generation = generation;
    slot.state = 'starting';
    slot.failure = undefined;
    let instance: T;
    try {
        instance = create(generation);
    } catch (error) {
        discardRuntimeSlot(slotName, slot, undefined, error);
        return completion;
    }
    slot.instance = instance;
    setCdpFFI(instance);

    void (async () => {
        try {
            await bootstrap(instance);
            if (!instance.isRuntimeGeneration(generation)) {
                slot.resolveCompletion?.(null);
                return;
            }
            instance.setRuntimeReady();
            slot.state = 'ready';
            slot.resolveCompletion?.(instance);
        } catch (error) {
            if (slot.instance === instance) {
                slot.state = 'failed';
                slot.failure = error;
                try {
                    await instance.destroy();
                } catch {
                    // Preserve the bootstrap failure if partial cleanup also fails.
                }
                clearCdpFFI(instance);
                slot.instance = undefined;
                discardRuntimeSlot(slotName, slot, undefined, error);
            }
        }
    })();
    return completion;
}
