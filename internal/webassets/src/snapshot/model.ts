export type SnapshotNodeKind = 'semantic' | 'text' | 'code' | 'table' | 'frame';

export interface SnapshotNode {
    id: number;
    kind: SnapshotNodeKind;
    depth?: number;
    role?: string;
    name?: string;
    text?: string;
    value?: string;
    language?: string;
    level?: number;
    states?: string[];
    rows?: string[][];
    runtime_id?: string;
    frame_url?: string;
    identity?: string;
}

export interface SnapshotDocument {
    id: number;
    url: string;
    title: string;
    lang?: string;
    revision: number;
    nodes: SnapshotNode[];
    warnings?: string[];
    next_id: number;
}

export interface SnapshotCaptureRequest {
    snapshot_id: number;
    next_id: number;
    depth: number;
}
