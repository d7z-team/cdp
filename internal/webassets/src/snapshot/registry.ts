export class SnapshotRegistry {
    private snapshotId = 0;
    private readonly elements = new Map<number, Element>();
    private readonly identities = new WeakMap<Element, number>();
    private nextIdentity = 1;

    begin(snapshotId: number): void {
        this.snapshotId = snapshotId;
        this.elements.clear();
    }

    register(snapshotId: number, elementId: number, element: Element): void {
        if (this.snapshotId !== snapshotId) {
            this.begin(snapshotId);
        }
        this.elements.set(elementId, element);
    }

    identity(element: Element): number {
        const existing = this.identities.get(element);
        if (existing !== undefined) return existing;
        const identity = this.nextIdentity++;
        this.identities.set(element, identity);
        return identity;
    }

    resolve(snapshotId: number, elementId: number): Element | null {
        if (snapshotId !== this.snapshotId) {
            return null;
        }
        const element = this.elements.get(elementId) || null;
        if (!element?.isConnected) {
            if (element) this.elements.delete(elementId);
            return null;
        }
        return element;
    }

    dispose(): void {
        this.snapshotId = 0;
        this.elements.clear();
    }
}
