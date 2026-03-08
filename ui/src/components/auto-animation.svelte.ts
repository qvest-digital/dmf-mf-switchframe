import { smoothstep } from '$lib/util/easing';

/**
 * Self-contained rAF animation for T-bar auto transitions.
 * Reactive state ($state) allows Svelte components to derive values from position.
 */
export class AutoAnimation {
	active = $state(false);
	position = $state(0);
	startTime = 0;
	duration = 0;
	private cancelled = false;
	private easingFn: (t: number) => number = smoothstep;

	start(durationMs: number, easingFn?: (t: number) => number) {
		this.cancelled = false;
		this.active = true;
		this.startTime = performance.now();
		this.duration = durationMs;
		this.position = 0;
		this.easingFn = easingFn ?? smoothstep;
		this.scheduleFrame();
	}

	stop() {
		this.cancelled = true;
		this.active = false;
		this.position = 0;
	}

	private scheduleFrame() {
		requestAnimationFrame(() => this.tick());
	}

	private tick() {
		if (this.cancelled || !this.active) return;
		const elapsed = performance.now() - this.startTime;
		const linear = Math.min(elapsed / this.duration, 1.0);
		this.position = this.easingFn(linear);
		if (linear < 1.0) {
			this.scheduleFrame();
		}
	}
}
