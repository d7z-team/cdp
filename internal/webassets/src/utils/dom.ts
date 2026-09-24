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

        const isReady = () => document.readyState === 'complete' || (document.readyState === 'interactive' && document.body);

        if (isReady()) {
            setTimeout(resolve, 0);
            return;
        }

        // 设置超时处理
        let timeoutId: ReturnType<typeof setTimeout> | null = null;

        if (timeout > 0) {
            timeoutId = setTimeout(() => {
                const message = `DOM ready timeout after ${timeout}ms`;
                if (forceResolve) {
                    console.warn(`${message}, forcing resolve`);
                    resolve();
                } else {
                    reject(new Error(message));
                }

                document.removeEventListener('DOMContentLoaded', handleLoad);
                window.removeEventListener('load', handleLoad);
            }, timeout);
        }

        const handleLoad = (): void => {
            if (timeoutId) {
                clearTimeout(timeoutId);
                timeoutId = null;
            }
            resolve();
        };

        document.addEventListener('DOMContentLoaded', handleLoad, {once: true});
        window.addEventListener('load', handleLoad, {once: true});
    });
}

export function composedParentElement(node: Element): Element | null {
    if (node.parentElement) {
        return node.parentElement;
    }
    const root = node.getRootNode();
    return root instanceof ShadowRoot ? root.host : null;
}
