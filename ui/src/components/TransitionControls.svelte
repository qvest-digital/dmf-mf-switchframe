<script lang="ts">
	import type { ControlRoomState, EasingConfig } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import { cut, startTransition, setTransitionPosition, fadeToBlack, listStingers, uploadStinger, deleteStinger, apiCall } from '$lib/api/switch-api';
	import { AutoAnimation } from './auto-animation.svelte';
	import { throttle } from '$lib/util/throttle';
	import { scrubberPosition, applyKeyStep } from '$lib/util/tbar';
	import type { EasingPreset } from '$lib/util/easing';
	import { getEasingFunction } from '$lib/util/easing';

	interface Props {
		state: ControlRoomState;
		pendingConfirm?: string | null;
		fastControl?: FastControl | null;
	}
	let { state: crState, pendingConfirm = null, fastControl = null }: Props = $props();

	type TransType = 'mix' | 'dip' | 'wipe' | 'stinger';
	type WipeDir = 'h-left' | 'h-right' | 'v-top' | 'v-bottom' | 'box-center-out' | 'box-edges-in';

	let transType = $state<TransType>('mix');
	let durationMs = $state(1000);
	let wipeDirection = $state<WipeDir>('h-left');
	let stingerName = $state('');
	let stingerNames = $state<string[]>([]);
	let uploading = $state(false);
	let fileInput = $state<HTMLInputElement>();
	let showDeleteConfirm = $state('');
	let easingPreset = $state<EasingPreset>('smoothstep');
	let customBezier = $state({ x1: 0.25, y1: 0.1, x2: 0.25, y2: 1.0 });

	/** Local guard: prevents duplicate startTransition() calls while awaiting server confirmation. */
	let tbarStarting = $state(false);

	/** Local drag position for instant visual feedback during pointer drag. null = use server state. */
	let dragPosition = $state<number | null>(null);

	/** Ref to scrubber track for accurate position calculation independent of padding. */
	let scrubberTrack = $state<HTMLElement>();

	/** Brief hold at 1.0 after transition completes to prevent rubber-band snap. */
	let completionHold = $state(false);
	let prevInTransition = false;

	/** Prevents re-starting a transition after one completes during the same drag gesture. */
	let dragSessionDone = false;

	/** Idle timer for wheel/trackpad scrub sessions. */
	let wheelIdleTimer: ReturnType<typeof setTimeout> | null = null;

	// Clear guard once server confirms the transition is active
	$effect(() => {
		if (crState.inTransition) {
			tbarStarting = false;
		}
	});

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

	function getEasingConfig(): EasingConfig | undefined {
		if (easingPreset === 'smoothstep') return undefined; // server default
		if (easingPreset === 'custom') {
			return { type: 'custom', x1: customBezier.x1, y1: customBezier.y1, x2: customBezier.x2, y2: customBezier.y2 };
		}
		return { type: easingPreset };
	}

	const autoDisabled = $derived(
		!crState.previewSource || crState.inTransition || crState.ftbActive ||
		(transType === 'stinger' && !stingerName)
	);

	const ftbDisabled = $derived(
		crState.inTransition && !crState.ftbActive
	);

	const tbarValue = $derived(
		dragPosition !== null ? dragPosition :
		completionHold ? 1.0 :
		anim.active ? anim.position :
		(crState.inTransition ? crState.transitionPosition : 0)
	);

	// Detect transition completion (falling edge) and hold scrubber at 1.0 briefly
	$effect(() => {
		const inTrans = crState.inTransition;
		if (prevInTransition && !inTrans) {
			if (anim.active) anim.stop();
			completionHold = true;
			dragSessionDone = true;
			setTimeout(() => { completionHold = false; }, 300);
		}
		prevInTransition = inTrans;
	});

	function handleAuto() {
		if (autoDisabled) return;
		const easingFn = getEasingFunction(easingPreset, customBezier.x1, customBezier.y1, customBezier.x2, customBezier.y2);
		anim.start(durationMs, easingFn);
		apiCall(startTransition(
			crState.previewSource, transType, durationMs,
			transType === 'wipe' ? wipeDirection : undefined,
			transType === 'stinger' ? stingerName : undefined,
			getEasingConfig()
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

	async function handleUpload() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		const name = file.name.replace(/\.zip$/i, '');
		uploading = true;
		try {
			await uploadStinger(name, file);
			const names = await listStingers();
			stingerNames = names;
			if (!stingerName && names.length > 0) stingerName = names[0];
		} catch (err) {
			apiCall(Promise.reject(err), 'Upload stinger');
		} finally {
			uploading = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function handleDeleteStinger(name: string) {
		try {
			await deleteStinger(name);
			stingerNames = stingerNames.filter(n => n !== name);
			if (stingerName === name) stingerName = stingerNames[0] ?? '';
		} catch (err) {
			apiCall(Promise.reject(err), 'Delete stinger');
		}
		showDeleteConfirm = '';
	}

	/** Throttled scrubber position update -- 16ms (~60fps). Uses datagrams when available,
	 *  falls back to REST. Silently drops errors from trailing-edge calls that arrive
	 *  after transition completes. */
	const setPositionThrottled = throttle((value: number) => {
		if (!crState.inTransition) return;
		if (fastControl) {
			fastControl.sendTransitionPosition(value);
		} else {
			setTransitionPosition(value).catch(() => {
				// Trailing-edge throttle fire after transition completed -- benign, ignore.
			});
		}
	}, 16);

	function handleScrubberPointerDown(e: PointerEvent) {
		const target = e.currentTarget as HTMLElement;
		target.setPointerCapture(e.pointerId);
		dragSessionDone = false;
		updateScrubberFromPointer(e);

		const onMove = (ev: PointerEvent) => updateScrubberFromPointer(ev);
		const onUp = () => {
			// Confirm final position via REST if using datagrams
			if (fastControl && crState.inTransition && dragPosition !== null) {
				setTransitionPosition(dragPosition).catch(() => {});
			}
			dragPosition = null;
			dragSessionDone = false;
			target.removeEventListener('pointermove', onMove);
			target.removeEventListener('pointerup', onUp);
			target.removeEventListener('pointercancel', onUp);
		};
		target.addEventListener('pointermove', onMove);
		target.addEventListener('pointerup', onUp);
		target.addEventListener('pointercancel', onUp);
	}

	function updateScrubberFromPointer(e: PointerEvent) {
		if (!scrubberTrack) return;
		const rect = scrubberTrack.getBoundingClientRect();
		const x = scrubberPosition(e.clientX, rect.left, rect.width);
		anim.active = false;
		dragPosition = x;

		// Don't start new transitions or send positions after one completed in this drag
		if (dragSessionDone) return;

		if (!crState.inTransition && !tbarStarting && x > 0 && crState.previewSource) {
			tbarStarting = true;
			const p = startTransition(
				crState.previewSource, transType, durationMs,
				transType === 'wipe' ? wipeDirection : undefined,
				transType === 'stinger' ? stingerName : undefined,
				getEasingConfig()
			);
			p.catch(() => { tbarStarting = false; });
			apiCall(p, 'Transition failed');
		}
		setPositionThrottled(x);
	}

	function handleWheel(e: WheelEvent) {
		e.preventDefault();

		// Use whichever axis has more motion (vertical swipe or horizontal swipe)
		let delta = Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY;
		// Normalize deltaMode: 0=pixels, 1=lines, 2=pages
		if (e.deltaMode === 1) delta *= 16;
		if (e.deltaMode === 2) delta *= 100;

		// 400px of cumulative scroll = full 0→1 transition
		const step = delta / 400;
		const currentPos = dragPosition ?? tbarValue;
		const newPos = Math.max(0, Math.min(1, currentPos + step));

		anim.active = false;
		dragPosition = newPos;

		if (!dragSessionDone) {
			if (!crState.inTransition && !tbarStarting && newPos > 0 && crState.previewSource) {
				tbarStarting = true;
				dragSessionDone = false;
				const p = startTransition(
					crState.previewSource, transType, durationMs,
					transType === 'wipe' ? wipeDirection : undefined,
					transType === 'stinger' ? stingerName : undefined,
					getEasingConfig()
				);
				p.catch(() => { tbarStarting = false; });
				apiCall(p, 'Transition failed');
			}
			if (newPos > 0) {
				setPositionThrottled(newPos);
			}
		}

		// Reset after scroll idle
		if (wheelIdleTimer) clearTimeout(wheelIdleTimer);
		wheelIdleTimer = setTimeout(() => {
			if (fastControl && crState.inTransition && dragPosition !== null) {
				setTransitionPosition(dragPosition).catch(() => {});
			}
			dragPosition = null;
			dragSessionDone = false;
			wheelIdleTimer = null;
		}, 200);
	}

	function handleScrubberKeydown(e: KeyboardEvent) {
		const newValue = applyKeyStep(tbarValue, e.key, e.shiftKey);
		if (newValue === tbarValue) return;
		e.preventDefault();
		anim.active = false;
		dragPosition = newValue;
		// Clear local override after a short delay so server state takes over
		setTimeout(() => { dragPosition = null; }, 100);

		if (!crState.inTransition && !tbarStarting && newValue > 0 && crState.previewSource) {
			tbarStarting = true;
			const p = startTransition(
				crState.previewSource, transType, durationMs,
				transType === 'wipe' ? wipeDirection : undefined,
				transType === 'stinger' ? stingerName : undefined,
				getEasingConfig()
			);
			p.catch(() => { tbarStarting = false; });
			apiCall(p, 'Transition failed');
		}
		if (newValue > 0) {
			setPositionThrottled(newValue);
		}
	}
</script>

<div class="transition-controls">
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

		<div class="type-specific">
			{#if transType === 'stinger'}
				<div class="stinger-controls">
					{#if showDeleteConfirm}
						<span class="delete-label">Del "{showDeleteConfirm}"?</span>
						<button class="confirm-yes" onclick={() => handleDeleteStinger(showDeleteConfirm)}>Y</button>
						<button class="confirm-no" onclick={() => showDeleteConfirm = ''}>N</button>
					{:else}
						<select class="stinger-select" aria-label="Stinger clip" bind:value={stingerName}>
							{#each stingerNames as name}
								<option value={name}>{name}</option>
							{/each}
							{#if stingerNames.length === 0}
								<option value="" disabled>None</option>
							{/if}
						</select>
						<button
							class="stinger-action-btn"
							onclick={() => fileInput?.click()}
							disabled={uploading}
							title="Upload stinger (.zip)"
							aria-label="Upload stinger"
						>{uploading ? '…' : '↑'}</button>
						{#if stingerName}
							<button
								class="stinger-action-btn stinger-delete-btn"
								onclick={() => showDeleteConfirm = stingerName}
								title="Delete {stingerName}"
								aria-label="Delete stinger"
							>✕</button>
						{/if}
					{/if}
					<input
						bind:this={fileInput}
						type="file"
						accept=".zip"
						onchange={handleUpload}
						style="display:none"
					/>
				</div>
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
		</div>

		<div class="timing-row">
			<select class="duration-select" aria-label="Transition duration" bind:value={durationMs}>
				<option value={500}>0.5s</option>
				<option value={1000}>1.0s</option>
				<option value={1500}>1.5s</option>
				<option value={2000}>2.0s</option>
				<option value={3000}>3.0s</option>
			</select>

			<select class="easing-select" aria-label="Easing curve" bind:value={easingPreset}>
				<option value="smoothstep">Smooth</option>
				<option value="linear">Linear</option>
				<option value="ease">Ease</option>
				<option value="ease-in">Ease In</option>
				<option value="ease-out">Ease Out</option>
				<option value="ease-in-out">Ease In/Out</option>
				<option value="custom">Custom</option>
			</select>
		</div>

		{#if easingPreset === 'custom'}
			<div class="custom-bezier">
				<label class="bezier-input">
					x1
					<input type="number" min="0" max="1" step="0.01" bind:value={customBezier.x1} />
				</label>
				<label class="bezier-input">
					y1
					<input type="number" min="-2" max="2" step="0.01" bind:value={customBezier.y1} />
				</label>
				<label class="bezier-input">
					x2
					<input type="number" min="0" max="1" step="0.01" bind:value={customBezier.x2} />
				</label>
				<label class="bezier-input">
					y2
					<input type="number" min="-2" max="2" step="0.01" bind:value={customBezier.y2} />
				</label>
			</div>
		{/if}
	</div>

	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="scrubber"
		role="slider"
		aria-label="Transition position"
		aria-valuemin={0}
		aria-valuemax={1}
		aria-valuenow={tbarValue}
		tabindex="0"
		onpointerdown={handleScrubberPointerDown}
		onkeydown={handleScrubberKeydown}
		onwheel={handleWheel}
	>
		<div class="scrubber-track" bind:this={scrubberTrack}>
			<div class="scrubber-fill" style="width: {tbarValue * 100}%"></div>
			<div class="scrubber-thumb" style="left: {tbarValue * 100}%"></div>
		</div>
	</div>
</div>

<style>
	.transition-controls {
		display: flex;
		flex-direction: column;
		gap: 3px;
		padding: 0 6px;
		border-left: 1px solid var(--border-default);
		margin-left: auto;
		width: 220px;
		flex-shrink: 0;
	}

	.transition-buttons {
		display: flex;
		gap: 3px;
	}

	.btn {
		padding: 4px 12px;
		border: 1.5px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-sm);
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
		font-size: var(--text-2xs);
		font-family: var(--font-mono);
		font-weight: 400;
		opacity: 0.3;
		margin-top: 1px;
		letter-spacing: 0;
	}

	.transition-options {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.type-specific:empty {
		display: none;
	}

	.timing-row {
		display: flex;
		gap: 4px;
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
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-secondary);
		cursor: pointer;
		display: flex;
		align-items: center;
		gap: 0;
		padding: 2px 8px;
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
		font-size: var(--text-sm);
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
		font-size: var(--text-xs);
		font-weight: 500;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		cursor: pointer;
		max-width: 100px;
		transition: border-color var(--transition-fast);
	}

	.stinger-select:hover {
		border-color: var(--border-strong);
	}

	.stinger-select:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.stinger-controls {
		display: flex;
		gap: 3px;
		align-items: center;
	}

	.stinger-action-btn {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		line-height: 1;
		padding: 2px 5px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		transition:
			border-color var(--transition-fast),
			color var(--transition-fast);
	}

	.stinger-action-btn:hover:not(:disabled) {
		border-color: var(--border-strong);
		color: var(--text-primary);
	}

	.stinger-action-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	.stinger-delete-btn:hover:not(:disabled) {
		border-color: var(--tally-program);
		color: var(--tally-program);
	}

	.delete-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		max-width: 100px;
	}

	.confirm-yes {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		padding: 2px 6px;
		border: 1px solid var(--tally-program);
		border-radius: var(--radius-sm);
		background: var(--tally-program-dim);
		color: var(--tally-program);
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.confirm-yes:hover {
		background: var(--tally-program);
		color: #fff;
	}

	.confirm-no {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}

	.confirm-no:hover {
		border-color: var(--border-strong);
	}

	.duration-select {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
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

	.easing-select {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		padding: 3px 6px;
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}

	.easing-select:hover {
		border-color: var(--border-strong);
	}

	.easing-select:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.custom-bezier {
		display: flex;
		gap: 4px;
		align-items: center;
	}

	.bezier-input {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-secondary);
	}

	.bezier-input input {
		width: 48px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg-elevated);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		transition: border-color var(--transition-fast);
	}

	.bezier-input input:hover {
		border-color: var(--border-strong);
	}

	.bezier-input input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.scrubber {
		cursor: grab;
		touch-action: none;
		padding: 6px 8px;
	}

	.scrubber:active { cursor: grabbing; }

	.scrubber-track {
		height: 10px;
		width: 100%;
		background: var(--bg-control);
		border: 1px solid var(--border-subtle);
		border-radius: 5px;
		position: relative;
	}

	.scrubber-fill {
		position: absolute;
		top: 0;
		left: 0;
		bottom: 0;
		background: var(--accent-yellow);
		border-radius: 5px 0 0 5px;
		transition: none;
	}

	.scrubber-thumb {
		position: absolute;
		top: -5px;
		width: 20px;
		height: 20px;
		background: var(--text-primary);
		border: 2px solid var(--bg-surface);
		border-radius: 50%;
		box-shadow: 0 1px 4px rgba(0, 0, 0, 0.6);
		transform: translateX(-50%);
	}
</style>
