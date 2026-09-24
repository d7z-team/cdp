export interface SelectorPlan {
    layers: SelectorPlanLayer[];
    terminal?: SelectorPlanTerminal;
}

export interface SelectorPlanLayer {
    options?: SelectorPlanOption[];
    filter?: SelectorPlanTerminal;
}

export interface SelectorPlanOption {
    kind: 'selector' | 'frame_enter';
    raw?: string;
}

export interface SelectorPlanTerminal {
    kind?: 'nth' | 'last';
    index?: number;
}

interface ParsedSelector {
    type: string;
    selector: string;
    name?: string;
    exact?: boolean;
}

export interface ParsedSelectorSegment extends ParsedSelector {
    raw: string;
}

const FRAME_ENTER_TOKEN = '|iframe|';

function selectorLiteral(value: string): string {
    if (value.startsWith('"') && value.endsWith('"')) {
        try {
            const decoded: unknown = JSON.parse(value);
            if (typeof decoded === 'string') return decoded;
        } catch {
            // Raw selectors also allow non-JSON quoted text.
        }
    }
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
        return value.slice(1, -1);
    }
    return value;
}

function parseSelectorSyntax(raw: string): ParsedSelector {
    if (raw === FRAME_ENTER_TOKEN) {
        return {type: 'frame_enter', selector: raw};
    }
    if (raw.startsWith('xpath=')) {
        return {type: 'xpath', selector: raw.slice(6)};
    }
    if (raw.startsWith('xpath/')) {
        return {type: 'xpath', selector: raw.slice(6)};
    }
    if (raw.startsWith('//') || raw.startsWith('(')) {
        return {type: 'xpath', selector: raw};
    }
    if (raw.startsWith('css=')) {
        return {type: 'css', selector: raw.slice(4)};
    }
    if (raw.startsWith('id=')) {
        return {type: 'css', selector: '#' + raw.slice(3)};
    }
    if (raw.startsWith('text-is=')) {
        return {type: 'text', selector: selectorLiteral(raw.slice(8)), exact: true};
    }
    if (raw.startsWith('text=')) {
        return {type: 'text', selector: selectorLiteral(raw.slice(5))};
    }
    if (raw.startsWith('text/')) {
        return {type: 'text', selector: raw.slice(5)};
    }
    if (raw.startsWith('has-text-is=')) {
        return {type: 'has-text', selector: selectorLiteral(raw.slice(12)), exact: true};
    }
    if (raw.startsWith('has-text=')) {
        return {type: 'has-text', selector: selectorLiteral(raw.slice(9))};
    }
    if (raw.startsWith('role=')) {
        const rolePart = raw.slice(5);
        const nameIndex = rolePart.indexOf('[name=');
        if (nameIndex !== -1) {
            return {
                type: 'role',
                selector: rolePart.slice(0, nameIndex),
                name: selectorLiteral(rolePart.slice(nameIndex + 6).replace(/\]$/, '')),
            };
        }
        return {type: 'role', selector: rolePart};
    }
    if (raw.startsWith('role/')) {
        return {type: 'role', selector: raw.slice(5)};
    }
    if (raw.startsWith('testid=')) {
        return {type: 'testid', selector: raw.slice(7)};
    }
    if (raw.startsWith('testid/')) {
        return {type: 'testid', selector: raw.slice(7)};
    }
    if (raw.startsWith('data-testid=')) {
        return {type: 'testid', selector: raw.slice(12)};
    }
    if (raw.startsWith('placeholder-is=')) {
        return {type: 'placeholder', selector: selectorLiteral(raw.slice(15)), exact: true};
    }
    if (raw.startsWith('label-is=')) {
        return {type: 'label', selector: selectorLiteral(raw.slice(9)), exact: true};
    }
    if (raw.startsWith('placeholder=')) {
        return {type: 'placeholder', selector: selectorLiteral(raw.slice(12))};
    }
    if (raw.startsWith('placeholder/')) {
        return {type: 'placeholder', selector: selectorLiteral(raw.slice(12))};
    }
    if (raw.startsWith('internal:role=')) {
        return parseSelectorSyntax(raw.slice(14));
    }
    if (raw.startsWith('internal:text=')) {
        return parseSelectorSyntax(raw.slice(14));
    }
    if (raw.startsWith('internal:testid=')) {
        return parseSelectorSyntax(raw.slice(16));
    }
    if (raw.startsWith('internal:placeholder=')) {
        return parseSelectorSyntax(raw.slice(21));
    }
    if (raw.startsWith('internal:label=')) {
        return {type: 'label', selector: raw.slice(15)};
    }
    if (raw.startsWith('label=')) {
        return {type: 'label', selector: selectorLiteral(raw.slice(6))};
    }
    if (raw.startsWith('label/')) {
        return {type: 'label', selector: selectorLiteral(raw.slice(6))};
    }
    return {type: '', selector: raw};
}

export function parseSelectorPath(raw: string): ParsedSelectorSegment[] {
    const segments: ParsedSelectorSegment[] = [];
    for (const part of raw.split(' >> ')) {
        const trimmed = part.trim();
        if (!trimmed) {
            continue;
        }
        const parsed = parseSelectorSyntax(trimmed);
        segments.push({...parsed, raw: trimmed});
    }
    return segments;
}

export function compileRawSelectorPlan(raw: string): SelectorPlan {
    return {
        layers: [{
            options: [{
                kind: 'selector',
                raw,
            }],
        }],
    };
}
