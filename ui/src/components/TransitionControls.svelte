<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { cut, startTransition, setTransitionPosition, fadeToBlack, listStingers, apiCall } from '$lib/api/switch-api';
	import { AutoAnimation } from './auto-animation.svelte';
	import { throttle } from '$lib/util/throttle';

	interface Props {
		state: ControlRoomState;
		pendingConfirm?: string | null;
	}
	let { state: crState, pendingConfirm = null }: Props = $props();

	type TransType = 'mix' | 'dip' | 'wipe' | 'stinger';
	type WipeDir = 'h-left' | 'h-right' | 'v-top' | 'v-bottom' | 'box-center-out' | 'box-edges-in';

	let transType = $state<TransType>('mix');
	let durationMs = $state(1000);
	let wipeDirection = $state<WipeDir>('h-left');
	let stingerName = $state('');
	let stingerNames = $state<string[]>([]);

	// Load stinger list on mount and when type changes to stinger
	$effect(() => {
		if (transType === 'stinger') {
			listStingers().then(names => {
				stingerNames = names;
				if (names.length > 0 && !stingerName) {
					stingerName = names[0];
				}
			}).catch(err => {
				console.error('Failed to load stinger list:', err);
			});
		}
	});

	const anim = new AutoAnimation();

	const autoDisabled = $derived(
		!crState.previewSource || crState.inTransition || crState.ftbActive ||
		(transType === 'stinger' && !stingerName)
	);

	const ftbDisabled = $derived(
		crState.inTransition && !crState.ftbActive
	);

	const tbarValue = $derived(
		anim.active ? anim.position : (crState.inTransition ? crState.transitionPosition : 0)
	);

	// Stop animation when server reports transition ended
	$effect(() => {
		if (!crState.inTransition && anim.active) {
			anim.stop();
		}
	});

	function handleAuto() {
		if (autoDisabled) return;
		anim.start(durationMs);
		apiCall(startTransition(
			crState.previewSource, transType, durationMs,
			transType === 'wipe' ? wipeDirection : undefined,
			transType === 'stinger' ? stingerName : undefined
		), 'Transition failed');

		// Safety timeout: cancel animation if server never confirms
		const safeDuration = durationMs;
		setTimeout(() => {
			if (anim.active) anim.stop();
		}, safeDuration + 500);
	}

	function handleFTB() {
		if (ftbDisabled) return;
		apiCall(fadeToBlack(), 'FTB failed');
	}

	/** Throttled T-bar position API call -- max 20 calls/sec (50ms). Visual slider updates instantly. */
	const setPositionThrottled = throttle((value: number) => {
		apiCall(setTransitionPosition(value), 'T-bar failed');
	}, 50);

	function handleTbarPointerDown(e: PointerEvent) {
		const target = e.currentTarget as HTMLElement;
		target.setPointerCapture(e.pointerId);
		updateTbarFromPointer(e);

		const onMove = (ev: PointerEvent) => updateTbarFromPointer(ev);
		const onUp = () => {
			target.removeEventListener('pointermove', onMove);
			target.removeEventListener('pointerup', onUp);
		};
		target.addEventListener('pointermove', onMove);
		target.addEventListener('pointerup', onUp);
	}

	function updateTbarFromPointer(e: PointerEvent) {
		const target = e.currentTarget as HTMLElement;
		const rect = target.getBoundingClientRect();
		const y = Math.max(0, Math.min(1, (e.clientY - rect.top) / rect.height));
		anim.active = false;

		if (!crState.inTransition && y > 0 && crState.previewSource) {
			apiCall(startTransition(
				crState.previewSource, transType, durationMs,
				transType === 'wipe' ? wipeDirection : undefined,
				transType === 'stinger' ? stingerName : undefined
			), 'Transition failed');
		}
		setPositionThrottled(y);
	}

	function handleTbarKeydown(e: KeyboardEvent) {
		const step = e.shiftKey ? 0.1 : 0.01;
		let newValue = tbarValue;
		if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
			newValue = Math.min(1, tbarValue + step);
		} else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
			newValue = Math.max(0, tbarValue - step);
		} else if (e.key === 'Home') {
			newValue = 0;
		} else if (e.key === 'End') {
			newValue = 1;
		} else {
			return;
		}
		e.preventDefault();
		anim.active = false;

		if (!crState.inTransition && newValue > 0 && crState.previewSource) {
			apiCall(startTransition(
				crState.previewSource, transType, durationMs,
				transType === 'wipe' ? wipeDirection : undefined,
				transType === 'stinger' ? stingerName : undefined
			), 'Transition failed');
		}
		if (newValue > 0) {
			setPositionThrottled(newValue);
		}
	}
</script>

<div class="transition-controls">
	<div class="transition-main">
		<div class="transition-buttons">
			<button class="btn cut" class:confirming={pendingConfirm === 'cut'} onclick={() => apiCall(cut(crState.previewSource), 'Cut failed')} disabled={!crState.previewSource}>
				CUT
				<span class="shortcut">Space</span>
			</button>
			<button class="btn auto" onclick={handleAuto} disabled={autoDisabled}>
				AUTO
				<span class="shortcut">Enter</span>
			</button>
			<button class="btn ftb" class:active={crState.ftbActive} onclick={handleFTB} disabled={ftbDisabled}>
				FTB
				<span class="shortcut">F1</span>
			</button>
		</div>

		<div class="transition-options">
			<div class="type-selector" role="radiogroup" aria-label="Transition type">
				<label class="type-option" class:selected={transType === 'mix'}>
					<input type="radio" name="transType" value="mix" bind:group={transType} />
					Mix
				</label>
				<label class="type-option" class:selected={transType === 'dip'}>
					<input type="radio" name="transType" value="dip" bind:group={transType} />
					Dip
				</label>
				<label class="type-option" class:selected={transType === 'wipe'}>
					<input type="radio" name="transType" value="wipe" bind:group={transType} />
					Wipe
				</label>
				<label class="type-option" class:selected={transType === 'stinger'}>
					<input type="radio" name="transType" value="stinger" bind:group={transType} />
					Sting
				</label>
			</div>

			{#if transType === 'stinger'}
				<select class="stinger-select" aria-label="Stinger clip" bind:value={stingerName}>
					{#each stingerNames as name}
						<option value={name}>{name}</option>
					{/each}
					{#if stingerNames.length === 0}
						<option value="" disabled>No stingers loaded</option>
					{/if}
				</select>
			{/if}

			{#if transType === 'wipe'}
				<div class="wipe-directions" role="radiogroup" aria-label="Wipe direction">
					<button class="wipe-dir-btn" class:selected={wipeDirection === 'h-left'} onclick={() => wipeDirection = 'h-left'} title="Horizontal left-to-right">&#8594;</button>
					<button class="wipe-dir-btn" class:selected={wipeDirection === 'h-right'} onclick={() => wipeDirection = 'h-right'} title="Horizontal right-to-left">&#8592;</button>
					<button class="wipe-dir-btn" class:selected={wipeDirection === 'v-top'} onclick={() => wipeDirection = 'v-top'} title="Vertical top-to-bottom">&#8595;</button>
					<button class="wipe-dir-btn" class:selected={wipeDirection === 'v-bottom'} onclick={() => wipeDirection = 'v-bottom'} title="Vertical bottom-to-top">&#8593;</button>
					<button class="wipe-dir-btn" class:selected={wipeDirection === 'box-center-out'} onclick={() => wipeDirection = 'box-center-out'} title="Box center outward">&#9723;</button>
					<button class="wipe-dir-btn" class:selected={wipeDirection === 'box-edges-in'} onclick={() => wipeDirection = 'box-edges-in'} title="Box edges inward">&#9724;</button>
				</div>
			{/if}

			<select class="duration-select" aria-label="Transition duration" bind:value={durationMs}>
				<option value={500}>0.5s</option>
				<option value={1000}>1.0s</option>
				<option value={1500}>1.5s</option>
				<option value={2000}>2.0s</option>
				<option value={3000}>3.0s</option>
			</select>
		</div>
	</div>

	<div
		class="tbar"
		role="slider"
		aria-label="Transition position"
		aria-valuemin={0}
		aria-valuemax={1}
		aria-valuenow={tbarValue}
		tabindex="0"
		onpointerdown={handleTbarPointerDown}
		onkeydown={handleTbarKeydown}
	>
		<div class="tbar-track">
			<div class="tbar-fill" style="height: {tbarValue * 100}%"></div>
			<div class="tbar-thumb" style="top: {tbarValue * 100}%"></div>
		</div>
	</div>
</div>

<style>
	.transition-controls {
		display: flex;
		gap: 10px;
		padding: 6px 10px;
		border-top: 1px solid var(--border-subtle);
		align-items: stretch;
	}

	.transition-main {
		display: flex;
		flex-direction: column;
		gap: 4px;
		flex: 1;
	}

	.transition-buttons {
		display: flex;
		gap: 4px;
	}

	.btn {
		padding: 6px 14px;
		border: 1.5px solid var(--border-default);
		border-radius: var(--radius-md);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 0.8rem;
		letter-spacing: 0.04em;
		position: relative;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			box-shadow var(--transition-normal);
	}

	.btn:active:not(:disabled) {
		transform: scale(0.97);
	}

	.btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.btn.cut:not(:disabled):hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		box-shadow: 0 0 8px rgba(220, 38, 38, 0.15);
	}

	.btn.cut.confirming {
		animation: pulse-confirm 0.5s ease-in-out infinite;
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		box-shadow: 0 0 16px rgba(220, 38, 38, 0.4);
	}

	@keyframes pulse-confirm {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}

	.btn.auto:not(:disabled):hover {
		border-color: var(--accent-yellow);
		background: var(--accent-yellow-dim);
		box-shadow: 0 0 8px rgba(234, 179, 8, 0.15);
	}

	.btn.ftb:not(:disabled):hover {
		border-color: var(--accent-orange);
		background: var(--accent-orange-dim);
		box-shadow: 0 0 8px rgba(245, 158, 11, 0.15);
	}

	.btn.ftb.active {
		background: var(--accent-orange);
		color: #000;
		border-color: var(--accent-orange);
		box-shadow: 0 0 12px rgba(245, 158, 11, 0.4);
	}

	.shortcut {
		display: block;
		font-size: 0.5rem;
		font-family: var(--font-mono);
		font-weight: 400;
		opacity: 0.35;
		margin-top: 2px;
		letter-spacing: 0;
	}

	.transition-options {
		display: flex;
		gap: 8px;
		align-items: center;
	}

	.type-selector {
		display: flex;
		gap: 2px;
		background: var(--bg-base);
		border-radius: var(--radius-md);
		padding: 2px;
		border: 1px solid var(--border-subtle);
	}

	.type-option {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 500;
		color: var(--text-secondary);
		cursor: pointer;
		display: flex;
		align-items: center;
		gap: 0;
		padding: 3px 10px;
		border-radius: var(--radius-sm);
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.type-option:hover {
		color: var(--text-primary);
	}

	.type-option.selected {
		background: var(--bg-elevated);
		color: var(--accent-yellow);
	}

	.type-option input {
		display: none;
	}

	.wipe-directions {
		display: flex;
		gap: 2px;
		background: var(--bg-base);
		border-radius: var(--radius-md);
		padding: 2px;
		border: 1px solid var(--border-subtle);
	}

	.wipe-dir-btn {
		font-size: 0.75rem;
		line-height: 1;
		padding: 3px 6px;
		border: none;
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--text-secondary);
		cursor: pointer;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.wipe-dir-btn:hover {
		color: var(--text-primary);
	}

	.wipe-dir-btn.selected {
		background: var(--bg-elevated);
		color: var(--accent-yellow);
	}

	.stinger-select {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 500;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		padding: 3px 6px;
		cursor: pointer;
		max-width: 120px;
		transition: border-color var(--transition-fast);
	}

	.stinger-select:hover {
		border-color: var(--border-strong);
	}

	.stinger-select:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.duration-select {
		font-family: var(--font-mono);
		font-size: 0.7rem;
		font-weight: 500;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		padding: 3px 6px;
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}

	.duration-select:hover {
		border-color: var(--border-strong);
	}

	.duration-select:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.tbar {
		width: 40px;
		flex-shrink: 0;
		cursor: grab;
		touch-action: none;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 4px 0;
	}

	.tbar:active { cursor: grabbing; }

	.tbar-track {
		width: 8px;
		height: 100%;
		background: var(--bg-control);
		border: 1px solid var(--border-subtle);
		border-radius: 4px;
		position: relative;
	}

	.tbar-fill {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		background: var(--accent-yellow);
		border-radius: 4px 4px 0 0;
		transition: none;
	}

	.tbar-thumb {
		position: absolute;
		left: -8px;
		width: 24px;
		height: 6px;
		background: var(--text-primary);
		border: 1px solid var(--bg-surface);
		border-radius: 3px;
		box-shadow: 0 1px 4px rgba(0, 0, 0, 0.4);
		transform: translateY(-50%);
	}
</style>
