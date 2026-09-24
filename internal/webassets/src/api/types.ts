import type {ActionabilityDiagnostic, ActionabilityOptions} from "../locator";

export interface ICdpLocator {
    isVisible(el: Element): boolean;
    isStable(el: Element, options?: ActionabilityOptions): Promise<boolean>;
    isEnabled(el: Element): boolean;
    checkActionability(el: Element, options?: ActionabilityOptions): Promise<ActionabilityDiagnostic>;
}

export interface ICdpRun {
    alertAdd(msg: string, timeout?: number): void;
    alertRemove(): void;
    destroy(): void;
}
