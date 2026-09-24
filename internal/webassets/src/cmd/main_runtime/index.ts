import {VirtualFullscreenController} from "../../fullscreen";

type PrintRequest = {
    id: string;
    at: number;
};

type MainRuntime = {
    destroy(): void;
    fullscreenState(): { active: boolean; element: string };
    consumePrintRequest(): PrintRequest | null;
};

declare function __INSTALL_MAIN_CONTROL__(control: MainRuntime): void;

function describeElement(el: Element | null): string {
    if (!el) return '';
    const testID = el.getAttribute('data-testid');
    if (testID) return `[data-testid="${testID}"]`;
    const id = el.id ? `#${el.id}` : '';
    return `${el.tagName.toLowerCase()}${id}`;
}

class CdpMainRuntime implements MainRuntime {
    private readonly originalPrint: (() => void) | undefined;
    private readonly fullscreen: VirtualFullscreenController;
    private printRequests: PrintRequest[] = [];
    private destroyed = false;

    constructor() {
        this.originalPrint = typeof window.print === 'function' ? window.print.bind(window) : undefined;
        this.fullscreen = new VirtualFullscreenController(() => {});
        this.fullscreen.install();
        const runtime = this;
        window.print = function (): void {
            runtime.capturePrint();
        };
    }

    destroy(): void {
        if (this.destroyed) return;
        this.destroyed = true;
        this.fullscreen.destroy();
        if (this.originalPrint) {
            window.print = this.originalPrint;
        }
        this.printRequests = [];
    }

    fullscreenState(): { active: boolean; element: string } {
        const state = this.fullscreen.state();
        return {
            active: !!state.active,
            element: describeElement(state.element),
        };
    }

    consumePrintRequest(): PrintRequest | null {
        return this.printRequests.shift() || null;
    }

    private capturePrint(): void {
        this.printRequests.push({
            id: `${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`,
            at: Date.now(),
        });
        window.dispatchEvent(new Event('beforeprint'));
        queueMicrotask(() => window.dispatchEvent(new Event('afterprint')));
    }
}

__INSTALL_MAIN_CONTROL__(new CdpMainRuntime());
