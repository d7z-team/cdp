export type MouseVisualMode = 'strict' | 'fast';

interface PointMouseVisualAction {
    kind: 'move' | 'click' | 'double-click' | 'right-click' | 'wheel';
    x: number;
    y: number;
    mode: MouseVisualMode;
}

interface DragMouseVisualAction {
    kind: 'drag';
    fromX: number;
    fromY: number;
    toX: number;
    toY: number;
    mode: MouseVisualMode;
}

interface ButtonMouseVisualAction {
    kind: 'press' | 'release';
    x: number;
    y: number;
    button: 'left' | 'right' | 'middle';
    mode: MouseVisualMode;
}

export type MouseVisualAction = PointMouseVisualAction | DragMouseVisualAction | ButtonMouseVisualAction;
