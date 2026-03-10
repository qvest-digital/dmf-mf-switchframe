/** Map a pointer X coordinate to a 0-1 scrubber position, clamped. */
export function scrubberPosition(clientX: number, rectLeft: number, rectWidth: number): number {
	return Math.max(0, Math.min(1, (clientX - rectLeft) / rectWidth));
}

/** Apply a keyboard step to the current scrubber value, clamped to [0, 1]. */
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
