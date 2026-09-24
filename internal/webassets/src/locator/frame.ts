import {describeElement} from "./index";
import type {ActionabilityDiagnostic, ActionabilityOptions, FrameSegmentDiagnostic, RectSnapshot} from "./index";
import {diagnoseStability, diagnoseVisibility} from "./visibility";
import {deepElementFromPoint} from "../dom/shadow";
import type {FrameElement} from "../utils/frame_element";

export function cloneDiagnostic(diagnostic: ActionabilityDiagnostic): ActionabilityDiagnostic {
    return {
        ...diagnostic,
        localRect: diagnostic.localRect ? {...diagnostic.localRect} : undefined,
        topRect: diagnostic.topRect ? {...diagnostic.topRect} : undefined,
        retryPoint: diagnostic.retryPoint ? {...diagnostic.retryPoint} : undefined,
        stability: diagnostic.stability ? {
            ...diagnostic.stability,
            maxDelta: {...diagnostic.stability.maxDelta},
            samples: diagnostic.stability.samples.map((sample) => ({...sample})),
        } : undefined,
        frameChain: diagnostic.frameChain.map((segment) => ({
            ...segment,
            rect: segment.rect ? {...segment.rect} : undefined,
        })),
    };
}

export function makeUnavailableFrameDiagnostic(base: ActionabilityDiagnostic, detail: string): ActionabilityDiagnostic {
    return {
        ...cloneDiagnostic(base),
        actionable: false,
        kind: 'cross_origin_unreachable',
        summary: 'Frame diagnostic unavailable',
        detail,
    };
}

function rectSnapshotFromRect(rect: DOMRect): RectSnapshot {
    return {x: rect.left, y: rect.top, width: rect.width, height: rect.height};
}

function buildFrameSegment(
    frameElement: FrameElement,
    summary: string,
    detail: string,
    rect?: RectSnapshot,
): FrameSegmentDiagnostic {
    return {frameElement: describeElement(frameElement), summary, detail, rect};
}

function pointOutsideViewport(x: number, y: number): boolean {
    return x < 0 || x > window.innerWidth || y < 0 || y > window.innerHeight;
}

async function diagnoseFrameContainer(
    frameElement: FrameElement,
    options?: ActionabilityOptions,
    childCenterX?: number,
    childCenterY?: number,
): Promise<ActionabilityDiagnostic> {
    const frameLabel = describeElement(frameElement);
    let visibility = diagnoseVisibility(frameElement);
    let rect = frameElement.getBoundingClientRect();
    let rs = rectSnapshotFromRect(rect);
    if (!visibility.visible) {
        const culpritLabel = visibility.culprit && visibility.culprit !== frameElement ? describeElement(visibility.culprit) : '';
        return {
            actionable: false, kind: 'frame_chain_blocked', summary: 'Frame chain blocked',
            detail: culpritLabel ? `${culpritLabel} causes ${visibility.detail}` : visibility.detail,
            element: frameLabel, culprit: culpritLabel || undefined, localRect: rs, topRect: rs, frameChain: [],
        };
    }

    let stability: ActionabilityDiagnostic['stability'];
    if (!options?.skipStability) {
        const result = await diagnoseStability(frameElement, options);
        stability = {
            kind: result.kind,
            elapsedMs: Math.round(result.elapsedMs),
            maxDelta: {...result.maxDelta},
            samples: result.samples.map(rectSnapshotFromRect),
        };
        if (!result.stable) {
            const delta = result.maxDelta;
            return {
                actionable: false,
                kind: result.kind === 'detached' ? 'detached' : result.kind === 'unavailable' ? 'unavailable' : 'unstable',
                summary: result.kind === 'detached' ? 'Frame container is detached' : 'Frame container is unstable',
                detail: result.kind === 'detached'
                    ? 'iframe element was detached while waiting for stable geometry'
                    : `iframe element did not settle within ${Math.round(result.elapsedMs)}ms; max delta x=${delta.x.toFixed(2)}, y=${delta.y.toFixed(2)}, width=${delta.width.toFixed(2)}, height=${delta.height.toFixed(2)}`,
                element: frameLabel,
                localRect: result.rect ? rectSnapshotFromRect(result.rect) : rs,
                topRect: result.rect ? rectSnapshotFromRect(result.rect) : rs,
                stability,
                frameChain: [],
            } satisfies ActionabilityDiagnostic;
        }
    }

    visibility = diagnoseVisibility(frameElement);
    rect = visibility.rect || frameElement.getBoundingClientRect();
    rs = rectSnapshotFromRect(rect);
    if (!visibility.visible) {
        return {
            actionable: false, kind: 'frame_chain_blocked', summary: 'Frame chain blocked',
            detail: visibility.detail, element: frameLabel, localRect: rs, topRect: rs, stability, frameChain: [],
        };
    }
    let projectedCenterX: number;
    let projectedCenterY: number;
    if (childCenterX === undefined || childCenterY === undefined) {
        projectedCenterX = rect.left + rect.width / 2;
        projectedCenterY = rect.top + rect.height / 2;
    } else {
        projectedCenterX = childCenterX + rect.left + frameElement.clientLeft;
        projectedCenterY = childCenterY + rect.top + frameElement.clientTop;
    }
    if (pointOutsideViewport(projectedCenterX, projectedCenterY)) {
        if (options?.allowOutsideViewport && !options.scrollIntoView) {
            return {
                actionable: true, kind: 'ok',
                summary: 'Frame target is outside viewport',
                detail: `projected ${options.purpose || 'action'} target will be scrolled before dispatch`,
                element: frameLabel, localRect: rs, topRect: rs, stability,
                centerX: projectedCenterX, centerY: projectedCenterY, frameChain: [],
            } satisfies ActionabilityDiagnostic;
        }
        return {
            actionable: false, kind: 'outside_viewport',
            summary: 'Projected target center is outside viewport',
            detail: `center point (${projectedCenterX.toFixed(0)}, ${projectedCenterY.toFixed(0)}) is outside viewport ${window.innerWidth}x${window.innerHeight}`,
            element: frameLabel, localRect: rs, topRect: rs, stability,
            centerX: projectedCenterX, centerY: projectedCenterY, frameChain: [],
        } satisfies ActionabilityDiagnostic;
    }
    if (projectedCenterX < rect.left || projectedCenterX > rect.right || projectedCenterY < rect.top || projectedCenterY > rect.bottom) {
        if (options?.allowOutsideViewport && !options.scrollIntoView) {
            return {
                actionable: true, kind: 'ok',
                summary: 'Frame target is outside iframe bounds',
                detail: `${options.purpose || 'action'} target inside iframe will be scrolled before dispatch`,
                element: frameLabel, localRect: rs, topRect: rs, stability,
                centerX: projectedCenterX, centerY: projectedCenterY, frameChain: [],
            } satisfies ActionabilityDiagnostic;
        }
        return {
            actionable: false, kind: 'frame_chain_blocked',
            summary: 'Projected target center is outside iframe bounds',
            detail: `center point (${projectedCenterX.toFixed(0)}, ${projectedCenterY.toFixed(0)}) falls outside ${frameLabel}`,
            element: frameLabel, localRect: rs, topRect: rs, stability,
            centerX: projectedCenterX, centerY: projectedCenterY, frameChain: [],
        } satisfies ActionabilityDiagnostic;
    }
    const topElement = deepElementFromPoint(document, projectedCenterX, projectedCenterY);
    if (topElement !== frameElement) {
        return {
            actionable: false, kind: 'obscured', summary: 'Frame container is obscured',
            detail: `hit test at (${projectedCenterX.toFixed(0)}, ${projectedCenterY.toFixed(0)}) resolves to ${describeElement(topElement)}`,
            element: frameLabel, culprit: describeElement(topElement),
            localRect: rs, topRect: rs, stability,
            centerX: projectedCenterX, centerY: projectedCenterY, frameChain: [],
        } satisfies ActionabilityDiagnostic;
    }
    return {
        actionable: true, kind: 'ok', summary: 'Frame container is actionable',
        detail: '', element: frameLabel, localRect: rs, topRect: rs, stability,
        centerX: projectedCenterX, centerY: projectedCenterY, frameChain: [],
    } satisfies ActionabilityDiagnostic;
}

export async function applyFrameElementDiagnostic(
    frameElement: FrameElement,
    childDiagnostic: ActionabilityDiagnostic,
    options?: ActionabilityOptions,
): Promise<ActionabilityDiagnostic> {
    const diagnostic = cloneDiagnostic(childDiagnostic);
    let frameRect = frameElement.getBoundingClientRect();
    const projectedTopRect = diagnostic.topRect || diagnostic.localRect;
    const localRetryPoint = diagnostic.retryPoint ? {...diagnostic.retryPoint} : undefined;
    const applyProjection = () => {
        if (!projectedTopRect) {
            return;
        }
        diagnostic.topRect = {
            x: projectedTopRect.x + frameRect.left + frameElement.clientLeft,
            y: projectedTopRect.y + frameRect.top + frameElement.clientTop,
            width: projectedTopRect.width,
            height: projectedTopRect.height,
        };
        if (localRetryPoint) {
            diagnostic.retryPoint = {
                x: localRetryPoint.x + frameRect.left + frameElement.clientLeft,
                y: localRetryPoint.y + frameRect.top + frameElement.clientTop,
            };
        }
    };
    const childCenterX = projectedTopRect ? projectedTopRect.x + projectedTopRect.width / 2 : undefined;
    const childCenterY = projectedTopRect ? projectedTopRect.y + projectedTopRect.height / 2 : undefined;
    if (options?.scrollIntoView && childCenterX !== undefined && childCenterY !== undefined) {
        const centerX = childCenterX + frameRect.left + frameElement.clientLeft;
        const centerY = childCenterY + frameRect.top + frameElement.clientTop;
        if (pointOutsideViewport(centerX, centerY) && frameElement.scrollIntoView) {
            frameElement.scrollIntoView({behavior: 'instant', block: 'center', inline: 'center'});
            frameRect = frameElement.getBoundingClientRect();
        }
    }
    let frameDiagnostic = await diagnoseFrameContainer(frameElement, options, childCenterX, childCenterY);
    if (
        options?.repositionObscured
        && frameDiagnostic.kind === 'obscured'
        && frameElement.scrollIntoView
    ) {
        frameElement.scrollIntoView({behavior: 'instant', block: 'center', inline: 'center'});
        frameRect = frameElement.getBoundingClientRect();
        frameDiagnostic = await diagnoseFrameContainer(frameElement, options, childCenterX, childCenterY);
    }
    if (frameDiagnostic.localRect) {
        frameRect = new DOMRect(
            frameDiagnostic.localRect.x,
            frameDiagnostic.localRect.y,
            frameDiagnostic.localRect.width,
            frameDiagnostic.localRect.height,
        );
    }
    applyProjection();
    if (!frameDiagnostic.actionable) {
        diagnostic.actionable = false;
        diagnostic.kind = frameDiagnostic.kind;
        diagnostic.summary = frameDiagnostic.summary;
        diagnostic.detail = `${describeElement(frameElement)}: ${frameDiagnostic.detail || frameDiagnostic.summary}`;
        diagnostic.culprit = frameDiagnostic.culprit || describeElement(frameElement);
        diagnostic.stability = frameDiagnostic.stability;
        diagnostic.frameChain.push(buildFrameSegment(frameElement, diagnostic.summary, diagnostic.detail, {
            x: frameRect.left, y: frameRect.top, width: frameRect.width, height: frameRect.height,
        }));
        return diagnostic;
    }
    if (diagnostic.topRect) {
        diagnostic.centerX = diagnostic.topRect.x + diagnostic.topRect.width / 2;
        diagnostic.centerY = diagnostic.topRect.y + diagnostic.topRect.height / 2;
    }
    diagnostic.frameChain.push(buildFrameSegment(frameElement, 'Frame segment passed', 'iframe element is visible and hit-testable', {
        x: frameRect.left, y: frameRect.top, width: frameRect.width, height: frameRect.height,
    }));
    return diagnostic;
}
