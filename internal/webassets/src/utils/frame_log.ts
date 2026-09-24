import { isFrameElement } from "./frame_element";

function currentFrameTag(): string {
    try {
        const frameElement = window.frameElement;
        if (!isFrameElement(frameElement)) {
            return '';
        }
        const id = frameElement.id?.trim();
        if (id) {
            return `[iframe=${id}] `;
        }
        return '[iframe] ';
    } catch {
        return '';
    }
}

function currentURLTag(): string {
    try {
        return `[url=${window.location.href}] `;
    } catch {
        return '';
    }
}

export function withFrameTag(message: string): string {
    return `${currentFrameTag()}${currentURLTag()}${message}`;
}
