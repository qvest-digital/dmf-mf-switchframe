/** CSS cubic-bezier presets matching standard easing curves */
export const EASING_PRESETS: Record<string, { x1: number; y1: number; x2: number; y2: number }> = {
	'linear': { x1: 0, y1: 0, x2: 1, y2: 1 },
	'ease': { x1: 0.25, y1: 0.1, x2: 0.25, y2: 1.0 },
	'ease-in': { x1: 0.42, y1: 0, x2: 1.0, y2: 1.0 },
	'ease-out': { x1: 0, y1: 0, x2: 0.58, y2: 1.0 },
	'ease-in-out': { x1: 0.42, y1: 0, x2: 0.58, y2: 1.0 },
	'smoothstep': { x1: 0, y1: 0, x2: 1, y2: 1 }, // placeholder, use inline formula
};

export type EasingPreset = 'linear' | 'ease' | 'ease-in' | 'ease-out' | 'ease-in-out' | 'smoothstep' | 'custom';

/** Hermite smoothstep: t*(3-2t) */
export function smoothstep(t: number): number {
	return t * t * (3 - 2 * t);
}

/**
 * Create a CSS cubic-bezier easing function using Newton-Raphson solver.
 * Mirrors the Go implementation for consistent server/client easing.
 */
export function cubicBezier(x1: number, y1: number, x2: number, y2: number): (t: number) => number {
	const EPSILON = 1e-7;
	const MAX_ITERATIONS = 8;

	function sampleCurve(s: number, p1: number, p2: number): number {
		// Bernstein polynomial: ((a*s + b)*s + c)*s
		const a = 1 - 3 * p2 + 3 * p1;
		const b = 3 * p2 - 6 * p1;
		const c = 3 * p1;
		return ((a * s + b) * s + c) * s;
	}

	function sampleDerivative(s: number, p1: number, p2: number): number {
		const a = 1 - 3 * p2 + 3 * p1;
		const b = 3 * p2 - 6 * p1;
		const c = 3 * p1;
		return (3 * a * s + 2 * b) * s + c;
	}

	function solveForS(x: number): number {
		// Newton-Raphson iterations
		let s = x; // initial guess
		for (let i = 0; i < MAX_ITERATIONS; i++) {
			const residual = sampleCurve(s, x1, x2) - x;
			if (Math.abs(residual) < EPSILON) return s;
			const d = sampleDerivative(s, x1, x2);
			if (Math.abs(d) < EPSILON) break;
			s -= residual / d;
		}

		// Bisection fallback
		let lo = 0;
		let hi = 1;
		s = x;
		for (let i = 0; i < MAX_ITERATIONS; i++) {
			const val = sampleCurve(s, x1, x2);
			if (Math.abs(val - x) < EPSILON) return s;
			if (val < x) {
				lo = s;
			} else {
				hi = s;
			}
			s = (lo + hi) / 2;
		}
		return s;
	}

	return (t: number): number => {
		if (t <= 0) return 0;
		if (t >= 1) return 1;
		const s = solveForS(t);
		return sampleCurve(s, y1, y2);
	};
}

/** Get an easing function by preset name or custom control points */
export function getEasingFunction(
	preset: EasingPreset,
	x1?: number,
	y1?: number,
	x2?: number,
	y2?: number,
): (t: number) => number {
	if (preset === 'smoothstep') return smoothstep;
	if (preset === 'linear') return (t) => t;
	if (preset === 'custom' && x1 !== undefined && y1 !== undefined && x2 !== undefined && y2 !== undefined) {
		return cubicBezier(x1, y1, x2, y2);
	}
	const p = EASING_PRESETS[preset];
	if (p) return cubicBezier(p.x1, p.y1, p.x2, p.y2);
	return smoothstep; // fallback
}
