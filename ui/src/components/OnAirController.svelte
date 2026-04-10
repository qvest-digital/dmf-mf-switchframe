<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import OAStatusBar from './on-air/OAStatusBar.svelte';
	import OAProgramMonitor from './on-air/OAProgramMonitor.svelte';
	import OANextMonitor from './on-air/OANextMonitor.svelte';
	import OASchedulePanel from './on-air/OASchedulePanel.svelte';
	import OASourceGrid from './on-air/OASourceGrid.svelte';
	import OAActionControls from './on-air/OAActionControls.svelte';
	import OASlideOut from './on-air/OASlideOut.svelte';
	import OASettingsModal from './on-air/OASettingsModal.svelte';

	interface Props {
		crState: ControlRoomState;
		onCanvasReady: (preview: HTMLCanvasElement, program: HTMLCanvasElement) => void;
		onSwitchLayout: () => void;
		isDualEngine?: boolean;
		syncStatus?: string;
		onSyncClick?: () => void;
		onCut?: (source: string) => void;
		onAuto?: (source: string) => void;
		onBugToggle?: () => void;
		onFirePod?: () => void;
		onReturnToLive?: () => void;
		onHold?: () => void;
		onSkip?: () => void;
		onTakeNext?: () => void;
	}

	let {
		crState,
		onCanvasReady,
		onSwitchLayout,
		isDualEngine = false,
		syncStatus = 'disconnected',
		onSyncClick,
		onCut,
		onAuto,
		onBugToggle,
		onFirePod,
		onReturnToLive,
		onHold,
		onSkip,
		onTakeNext,
	}: Props = $props();

	let previewCanvas: HTMLCanvasElement;
	let programCanvas: HTMLCanvasElement;

	$effect(() => {
		if (previewCanvas && programCanvas && onCanvasReady) {
			onCanvasReady(previewCanvas, programCanvas);
		}
	});

	// High-DPI canvas sizing via ResizeObserver
	$effect(() => {
		if (!programCanvas) return;

		const observers: ResizeObserver[] = [];

		if (programCanvas.parentElement) {
			const obs = new ResizeObserver(([entry]) => {
				const { width, height } = entry.contentRect;
				if (width > 0 && height > 0) setupHiDPICanvas(programCanvas, width, height);
			});
			obs.observe(programCanvas.parentElement);
			observers.push(obs);
		}

		return () => observers.forEach((obs) => obs.disconnect());
	});

	// ── Derived state ──

	const hasPlaylist = $derived(
		(crState.playout?.channels?.[0]?.itemCount ?? 0) > 0
	);

	const programSourceLabel = $derived(
		crState.sources?.[crState.programSource]?.label ?? crState.programSource ?? 'No Source'
	);

	const bugActive = $derived(
		(crState.graphics?.layers ?? []).some((l) => l.active)
	);

	const scteState = $derived.by((): 'content' | 'in_break' | 'idle' => {
		const active = crState.scte35?.activeEvents ?? {};
		if (Object.keys(active).length > 0) return 'in_break';
		// If SCTE-35 is not enabled, show idle
		if (!crState.scte35?.enabled) return 'idle';
		return 'content';
	});

	const scteCountdown = $derived.by((): number | undefined => {
		const active = crState.scte35?.activeEvents ?? {};
		const first = Object.values(active)[0];
		if (!first || first.remainingMs == null) return undefined;
		return Math.max(0, first.remainingMs / 1000);
	});

	const playoutChannel = $derived(crState.playout?.channels?.[0]);

	const captionMode = $derived(
		(crState.captions?.mode ?? 'off') as 'off' | 'passthrough' | 'author'
	);

	const podArmed = $derived(
		(playoutChannel?.podsArmed ?? 0) > 0
	);

	const nextItemTitle = $derived(
		playoutChannel?.nextTitle ?? 'No item cued'
	);

	// ── Slide-out and modal state ──

	let audioSlideOut = $state(false);
	let ioSlideOut = $state(false);
	let settingsVisible = $state(false);

	// ── Keyboard shortcuts ──

	function handleKeydown(e: KeyboardEvent) {
		if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;

		switch (e.code) {
			case 'Space':
				e.preventDefault();
				if (hasPlaylist) onFirePod?.();
				else onCut?.(crState.previewSource);
				break;
			case 'Enter':
				e.preventDefault();
				if (hasPlaylist) onReturnToLive?.();
				else onAuto?.(crState.previewSource);
				break;
			case 'KeyB':
				e.preventDefault();
				onBugToggle?.();
				break;
			case 'KeyH':
				if (hasPlaylist) {
					e.preventDefault();
					onHold?.();
				}
				break;
			case 'KeyS':
				if (hasPlaylist) {
					e.preventDefault();
					onSkip?.();
				}
				break;
			case 'KeyN':
				if (hasPlaylist) {
					e.preventDefault();
					onTakeNext?.();
				}
				break;
			case 'Digit1': case 'Digit2': case 'Digit3': case 'Digit4':
			case 'Digit5': case 'Digit6': case 'Digit7': case 'Digit8': case 'Digit9': {
				if (!hasPlaylist) {
					e.preventDefault();
					const index = parseInt(e.code.replace('Digit', '')) - 1;
					const sourceKeys = Object.keys(crState.sources ?? {});
					if (index < sourceKeys.length) {
						onCut?.(sourceKeys[index]);
					}
				}
				break;
			}
			case 'Escape':
				audioSlideOut = false;
				ioSlideOut = false;
				settingsVisible = false;
				break;
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="on-air-controller">
	<OAStatusBar
		programSource={programSourceLabel}
		{bugActive}
		{scteState}
		{scteCountdown}
		{isDualEngine}
		{syncStatus}
		{onSyncClick}
		{onBugToggle}
		onModeMenu={onSwitchLayout}
		showUptime={!hasPlaylist}
	/>

	<div class="oa-main">
		<section class="oa-monitor-column">
			<OAProgramMonitor
				programSource={programSourceLabel}
				{isDualEngine}
				onCanvasReady={(canvas) => {
					programCanvas = canvas;
				}}
			/>
			<!-- Hidden preview canvas for pipeline -->
			<canvas bind:this={previewCanvas} class="hidden-canvas"></canvas>
			<OANextMonitor
				nextItemTitle={nextItemTitle}
			/>
		</section>

		<section class="oa-content-column">
			<div class="oa-content-area">
				{#if hasPlaylist && playoutChannel}
					<OASchedulePanel
						playoutChannel={playoutChannel}
					/>
				{:else}
					<OASourceGrid
						sources={crState.sources ?? {}}
						tallyState={crState.tallyState ?? {}}
						{isDualEngine}
						onSourceClick={(key) => onCut?.(key)}
						onOpenIO={() => { ioSlideOut = true; }}
					/>
				{/if}
			</div>

			<OAActionControls
				mode={hasPlaylist ? 'playout' : 'live-monitor'}
				{bugActive}
				{captionMode}
				{podArmed}
				inBreak={scteState === 'in_break'}
				holdActive={false}
				{onBugToggle}
				{onFirePod}
				{onReturnToLive}
				{onHold}
				{onSkip}
				{onTakeNext}
				onCut={() => onCut?.(crState.previewSource)}
				onAuto={() => onAuto?.(crState.previewSource)}
				onAudioToggle={() => { audioSlideOut = !audioSlideOut; }}
				onIOToggle={() => { ioSlideOut = !ioSlideOut; }}
				onSettingsOpen={() => { settingsVisible = true; }}
			/>
		</section>
	</div>

	<!-- Slide-outs -->
	<OASlideOut visible={audioSlideOut} title="AUDIO" onclose={() => { audioSlideOut = false; }}>
		<p style="padding: 12px; color: var(--text-secondary)">Audio mixer will be wired here</p>
	</OASlideOut>

	<OASlideOut visible={ioSlideOut} title="I/O" onclose={() => { ioSlideOut = false; }}>
		<p style="padding: 12px; color: var(--text-secondary)">I/O panel will be wired here</p>
	</OASlideOut>

	<OASettingsModal visible={settingsVisible} onclose={() => { settingsVisible = false; }} {crState} />
</div>

<style>
	.on-air-controller {
		display: grid;
		grid-template-rows: auto 1fr;
		height: 100vh;
		background: var(--bg-base);
		color: var(--text-primary);
		font-family: var(--font-ui);
		overflow: hidden;
	}

	.oa-main {
		display: grid;
		grid-template-columns: 40% 60%;
		gap: 1px;
		min-height: 0;
		background: var(--border-subtle);
	}

	.oa-monitor-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-base);
		min-height: 0;
	}

	.oa-content-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-base);
		min-height: 0;
	}

	.oa-content-area {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.hidden-canvas {
		position: absolute;
		width: 0;
		height: 0;
		overflow: hidden;
		pointer-events: none;
	}

	/* ── Responsive: Tablet (768-1023px) ── */
	@media (max-width: 1023px) {
		.oa-main {
			grid-template-columns: 1fr;
		}

		.oa-monitor-column {
			border-right: none;
			border-bottom: 1px solid var(--border-default);
			max-height: 40vh;
		}
	}

	/* ── Responsive: Mobile (<768px) ── */
	@media (max-width: 767px) {
		.oa-monitor-column {
			max-height: 30vh;
		}

		.oa-monitor-column :global(.oa-next-monitor) {
			display: none;
		}
	}

	/* ── Touch devices ── */
	@media (pointer: coarse) {
		.oa-main :global(.btn),
		.oa-main :global(.oa-source-tile),
		.oa-main :global(.grid-action-btn) {
			min-height: 44px;
			min-width: 44px;
		}
	}
</style>
