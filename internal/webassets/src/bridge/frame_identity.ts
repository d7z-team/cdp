import {isFrameElement, type FrameElement} from '../utils/frame_element';

// CDP supplies identities and genuine owner elements in this isolated realm.
// Page messages cannot write these associations, including for shadow frames
// that do not appear in Window's indexed child collection.
const owners = new WeakMap<Window, FrameElement>();

export function ownerFrameElement(target: Window): FrameElement | null {
    try {
        const owner = owners.get(target) ?? target.frameElement;
        return isFrameElement(owner) ? owner : null;
    } catch {
        return null;
    }
}

const identities = new WeakMap<Window, string>();

export function registerFrameIdentity(id: string, parentID: string): void {
    identities.set(window, `frame:${id}`);
    if (parentID && window.parent !== window) identities.set(window.parent, `frame:${parentID}`);
}

export function registerFrameWindow(frame: FrameElement, id: string): void {
    const child = frame.contentWindow;
    if (child) {
        identities.set(child, `frame:${id}`);
        owners.set(child, frame);
    }
}

export function registeredFrameIdentity(target: Window): string | undefined {
    return identities.get(target);
}
