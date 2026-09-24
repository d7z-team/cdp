export interface DomReadyOptions {
    timeout?: number;      // 超时时间（毫秒）
    forceResolve?: boolean; // 超时后是否强制解析
}

export function domReady(options: DomReadyOptions = {}): Promise<void> {
    const {
        timeout = 30000,
        forceResolve = true,
    } = options;

    return new Promise<void>((resolve, reject) => {
        if (typeof window === 'undefined' || typeof document === 'undefined') {
            reject(new Error('DOM is not available (non-browser environment)'));
            return;
        }

        if (document.readyState === 'complete' || (document.readyState === 'interactive' && document.body)) {
            setTimeout(resolve, 0);
            return;
        }

        let timeoutId: ReturnType<typeof setTimeout> | undefined;
        const finish = (error?: Error): void => {
            clearTimeout(timeoutId);
            document.removeEventListener('DOMContentLoaded', handleLoad);
            window.removeEventListener('load', handleLoad);
            if (error) reject(error);
            else resolve();
        };
        const handleLoad = (): void => finish();

        document.addEventListener('DOMContentLoaded', handleLoad, {once: true});
        window.addEventListener('load', handleLoad, {once: true});
        if (timeout > 0) {
            timeoutId = setTimeout(() => {
                finish(forceResolve ? undefined : new Error(`DOM ready timeout after ${timeout}ms`));
            }, timeout);
        }
    });
}

export function composedParentElement(node: Element): Element | null {
    if (node.parentElement) {
        return node.parentElement;
    }
    const root = node.getRootNode();
    return root instanceof ShadowRoot ? root.host : null;
}
