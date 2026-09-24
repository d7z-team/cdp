import {markInternalElement, unmarkInternalElement} from "../utils/internal_ui";

export function applyNonInteractive(element: HTMLElement | SVGElement): void {
    element.style.setProperty('pointer-events', 'none', 'important');
}

export function createOverlayContainer(root: ShadowRoot, id: string, cssText: string): HTMLDivElement {
    const container = document.createElement('div');
    container.id = id;
    markInternalElement(container);
    container.style.cssText = cssText;
    applyNonInteractive(container);
    root.appendChild(container);
    return container;
}

export function ensureOverlayHost(
    existingHost: HTMLDivElement | null,
    hostID: string,
    zIndex: number,
): { host: HTMLDivElement; root: ShadowRoot } {
    if (existingHost?.isConnected && existingHost.shadowRoot) {
        return {
            host: existingHost,
            root: existingHost.shadowRoot,
        };
    }

    let host = document.getElementById(hostID) as HTMLDivElement | null;
    if (!host) {
        host = document.createElement('div');
        host.id = hostID;
        Object.assign(host.style, {
            position: 'fixed',
            left: '0px',
            top: '0px',
            width: '0px',
            height: '0px',
            overflow: 'visible',
            zIndex: String(zIndex),
            pointerEvents: 'none',
        });
        applyNonInteractive(host);
        (document.body || document.documentElement).appendChild(host);
    }
    markInternalElement(host);

    return {
        host,
        root: host.shadowRoot ?? host.attachShadow({mode: 'open'}),
    };
}

export function removeElement(element: HTMLElement | null): void {
    unmarkInternalElement(element);
    element?.remove();
}
