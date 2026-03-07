/** Map a pointer Y coordinate to a 0-1 T-bar position, clamped. */
export function tbarPosition(clientY: number, rectTop: number, rectHeight: number): number {
	return Math.max(0, Math.min(1, (clientY - rectTop) / rectHeight));
}

/** Apply a keyboard step to the current T-bar value, clamped to [0, 1]. */
export function applyKeyStep(currentValue: number, key: string, shiftKey: boolean): number {
	const step = shiftKey ? 0.1 : 0.01;
	if (key === 'ArrowDown' || key === 'ArrowRight') {
		return Math.min(1, currentValue + step);
	} else if (key === 'ArrowUp' || key === 'ArrowLeft') {
		return Math.max(0, currentValue - step);
	} else if (key === 'Home') {
		return 0;
	} else if (key === 'End') {
		return 1;
	}
	return currentValue;
}
