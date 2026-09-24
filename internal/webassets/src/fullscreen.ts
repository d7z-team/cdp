type FullscreenChangeState = {
    active: boolean;
    element: Element | null;
};

type FullscreenChangeHandler = (state: FullscreenChangeState) => void;

type DescriptorRestore = {
    target: object;
    key: string;
    descriptor?: PropertyDescriptor;
};

const fullscreenChangeEvents = [
    'fullscreenchange',
    'webkitfullscreenchange',
    'mozfullscreenchange',
    'MSFullscreenChange',
] as const;

function restoreDescriptor(entry: DescriptorRestore): void {
    if (entry.descriptor) {
        Object.defineProperty(entry.target, entry.key, entry.descriptor);
        return;
    }
    delete (entry.target as Record<string, unknown>)[entry.key];
}

export class VirtualFullscreenController {
    private readonly restores: DescriptorRestore[] = [];
    private activeElement: Element | null = null;
    private installed = false;

    public constructor(private readonly onChange: FullscreenChangeHandler) {}

    public install(): void {
        if (this.installed) {
            return;
        }
        this.installed = true;

        const defineMethod = (target: object, key: string, handler: (thisArg: unknown, args: unknown[]) => unknown): void => {
            const descriptor = Object.getOwnPropertyDescriptor(target, key);
            this.restores.push({target, key, descriptor});
            Object.defineProperty(target, key, {
                configurable: true,
                enumerable: descriptor?.enumerable ?? false,
                writable: true,
                value: function (...args: unknown[]) {
                    return handler(this, args);
                },
            });
        };

        const defineGetter = (target: object, key: string, getter: () => unknown): void => {
            const descriptor = Object.getOwnPropertyDescriptor(target, key);
            this.restores.push({target, key, descriptor});
            Object.defineProperty(target, key, {
                configurable: true,
                enumerable: descriptor?.enumerable ?? false,
                get: getter,
            });
        };

        const request = (thisArg: unknown) => {
            this.applyState(thisArg instanceof Element ? thisArg : null, true);
            return Promise.resolve();
        };
        const exit = () => {
            this.applyState(null, true);
            return Promise.resolve();
        };

        defineMethod(Element.prototype, 'requestFullscreen', (thisArg) => request(thisArg));
        defineMethod(Document.prototype, 'exitFullscreen', () => exit());

        for (const key of ['webkitRequestFullscreen', 'webkitRequestFullScreen', 'mozRequestFullScreen', 'msRequestFullscreen']) {
            defineMethod(Element.prototype, key, (thisArg) => request(thisArg));
        }
        for (const key of ['webkitExitFullscreen', 'mozCancelFullScreen', 'msExitFullscreen']) {
            defineMethod(Document.prototype, key, () => exit());
        }

        for (const key of ['fullscreenElement', 'webkitFullscreenElement', 'webkitCurrentFullScreenElement', 'mozFullScreenElement', 'msFullscreenElement']) {
            defineGetter(Document.prototype, key, () => this.activeElement);
        }
        for (const key of ['fullscreenEnabled', 'webkitFullscreenEnabled', 'mozFullScreenEnabled', 'msFullscreenEnabled']) {
            defineGetter(Document.prototype, key, () => true);
        }
    }

    public destroy(): void {
        if (!this.installed) {
            return;
        }
        this.applyState(null, false);
        for (let i = this.restores.length - 1; i >= 0; i--) {
            restoreDescriptor(this.restores[i]);
        }
        this.restores.length = 0;
        this.installed = false;
    }

    public state(): FullscreenChangeState {
        return {
            active: this.activeElement !== null,
            element: this.activeElement,
        };
    }

    private applyState(element: Element | null, dispatchEvents: boolean): void {
        const previous = this.activeElement;
        if (previous === element) {
            this.onChange({active: element !== null, element});
            return;
        }
        this.activeElement = element;
        this.onChange({active: element !== null, element});
        if (!dispatchEvents) {
            return;
        }
        const targets = new Set<EventTarget>([document]);
        if (previous) {
            targets.add(previous);
        }
        if (element) {
            targets.add(element);
        }
        queueMicrotask(() => {
            for (const eventName of fullscreenChangeEvents) {
                const event = new Event(eventName);
                targets.forEach((target) => target.dispatchEvent(event));
            }
        });
    }
}
