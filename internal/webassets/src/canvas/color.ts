import {DESIGN_TOKENS} from "../design";

export function parseColor(color: string): { r: number; g: number; b: number } | null {
    if (color.startsWith('#')) {
        const hex = color.slice(1);
        if (hex.length === 3) {
            return {
                r: parseInt(hex[0] + hex[0], 16),
                g: parseInt(hex[1] + hex[1], 16),
                b: parseInt(hex[2] + hex[2], 16),
            };
        }
        if (hex.length === 6) {
            return {
                r: parseInt(hex.slice(0, 2), 16),
                g: parseInt(hex.slice(2, 4), 16),
                b: parseInt(hex.slice(4, 6), 16),
            };
        }
    }

    const rgbMatch = color.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/i);
    if (!rgbMatch) {
        return null;
    }

    return {
        r: parseInt(rgbMatch[1], 10),
        g: parseInt(rgbMatch[2], 10),
        b: parseInt(rgbMatch[3], 10),
    };
}

export function withAlpha(color: string, alpha: number): string {
    const rgb = parseColor(color);
    if (!rgb) {
        return color;
    }
    const clampedAlpha = Math.max(0, Math.min(1, alpha));
    return `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${clampedAlpha})`;
}

export function toToastAccent(color: string): string {
    const rgb = parseColor(color);
    if (!rgb) {
        return DESIGN_TOKENS.fallbackAccent;
    }

    const mix = (value: number, target: number, weight: number): number =>
        Math.round(value * (1 - weight) + target * weight);

    return `rgb(${mix(rgb.r, 37, 0.7)}, ${mix(rgb.g, 99, 0.7)}, ${mix(rgb.b, 235, 0.7)})`;
}
