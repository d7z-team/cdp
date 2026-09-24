import { isFrameElement } from "./frame_element";

export const INTERNAL_ID_PREFIX = '__cdp_';

const INTERNAL_FIXED_ID_PREFIXES = [INTERNAL_ID_PREFIX] as const;

export function internalElementID(name: string): string {
    return `${INTERNAL_ID_PREFIX}${name}`;
}

export function hasInternalFixedIDPrefix(id: string | null | undefined): boolean {
    if (!id) {
        return false;
    }
    return INTERNAL_FIXED_ID_PREFIXES.some((prefix) => id.startsWith(prefix));
}

export function shouldSkipBootstrapInCurrentFrame(): boolean {
    try {
        const frameElement = window.frameElement;
        if (!isFrameElement(frameElement)) {
            return false;
        }
        return hasInternalFixedIDPrefix(frameElement.id);
    } catch {
        return false;
    }
}
