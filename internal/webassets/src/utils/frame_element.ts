export type FrameElement = HTMLIFrameElement | HTMLFrameElement;

export function isFrameElement(value: unknown): value is FrameElement {
    if (!value || typeof value !== 'object' || !('tagName' in value) || typeof value.tagName !== 'string') {
        return false;
    }
    const tagName = value.tagName.toLowerCase();
    return tagName === 'iframe' || tagName === 'frame';
}
