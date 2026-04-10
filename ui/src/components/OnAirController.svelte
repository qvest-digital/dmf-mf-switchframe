<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';

	interface Props {
		crState: ControlRoomState;
		onCanvasReady: (preview: HTMLCanvasElement, program: HTMLCanvasElement) => void;
		onSwitchLayout: () => void;
		isDualEngine?: boolean;
		syncStatus?: string;
	}

	let { crState, onCanvasReady, onSwitchLayout, isDualEngine = false, syncStatus = 'disconnected' }: Props = $props();

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

	// Detect whether a playout playlist is loaded (channel 0)
	const hasPlaylist = $derived(
		(crState.playout?.channels?.[0]?.itemCount ?? 0) > 0
	);

	let programLabel = $derived(
		crState.programSource && crState.sources[crState.programSource]
			? crState.sources[crState.programSource].label || crState.programSource
			: '--',
	);

	let channelState = $derived(crState.playout?.channels?.[0]?.state ?? 'stopped');
</script>

<div class="on-air-controller">
	<!-- Status Bar -->
	<header class="status-bar">
		<div class="status-bar-left">
			<button class="mode-btn" onclick={onSwitchLayout} title="Switch layout mode">
				&#9881;
			</button>
			<span class="brand">SwitchFrame</span>
			<span class="mode-badge">ON AIR</span>
		</div>
		<div class="status-bar-right">
			{#if isDualEngine}
				<span class="sync-indicator" class:sync-ok={syncStatus === 'ok'} class:sync-warn={syncStatus === 'resyncing'} class:sync-error={syncStatus === 'disconnected'}>
					{syncStatus === 'ok' ? 'SYNC' : syncStatus === 'resyncing' ? 'RESYNC' : 'NO SYNC'}
				</span>
			{/if}
		</div>
	</header>

	<!-- Main Area: 2-column grid -->
	<div class="main-area">
		<!-- Left Column: Program Monitor -->
		<div class="program-column">
			<div class="program-monitor">
				<div class="monitor-label">PROGRAM</div>
				<canvas bind:this={programCanvas}></canvas>
				<div class="monitor-source">{programLabel}</div>
			</div>
			<!-- Hidden preview canvas for pipeline -->
			<canvas bind:this={previewCanvas} class="hidden-canvas"></canvas>
		</div>

		<!-- Right Column: Schedule or Sources -->
		<div class="right-column">
			<div class="right-content">
				{#if hasPlaylist}
					<div class="schedule-panel">
						<div class="panel-header">PLAYOUT SCHEDULE</div>
						<div class="panel-body">
							<span class="placeholder-text">
								Channel state: {channelState}
							</span>
						</div>
					</div>
				{:else}
					<div class="sources-panel">
						<div class="panel-header">SOURCES</div>
						<div class="panel-body">
							<span class="placeholder-text">
								{Object.keys(crState.sources).length} sources available
							</span>
						</div>
					</div>
				{/if}
			</div>

			<!-- Action controls at bottom of right column -->
			<div class="action-bar">
				<span class="placeholder-text">Actions</span>
			</div>
		</div>
	</div>
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

	/* ── Status Bar ── */
	.status-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 6px 16px;
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}

	.status-bar-left {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.status-bar-right {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.mode-btn {
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: var(--text-xl);
		cursor: pointer;
		padding: 4px 8px;
		border-radius: var(--radius-sm);
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast);
	}

	.mode-btn:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.brand {
		font-size: var(--text-md);
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.03em;
	}

	.mode-badge {
		padding: 2px 8px;
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		border-radius: var(--radius-sm);
		background: var(--tally-program);
		color: var(--text-on-color);
		text-transform: uppercase;
	}

	.sync-indicator {
		padding: 2px 8px;
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
	}

	.sync-ok {
		background: rgba(22, 163, 74, 0.2);
		color: var(--tally-preview);
	}

	.sync-warn {
		background: rgba(245, 158, 11, 0.2);
		color: var(--accent-orange);
	}

	.sync-error {
		background: rgba(220, 38, 38, 0.2);
		color: var(--color-error);
	}

	/* ── Main Area ── */
	.main-area {
		display: grid;
		grid-template-columns: 40% 60%;
		gap: 1px;
		min-height: 0;
		background: var(--border-subtle);
	}

	/* ── Program Column ── */
	.program-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-base);
		padding: 12px;
		min-height: 0;
	}

	.program-monitor {
		position: relative;
		flex: 1;
		aspect-ratio: 16 / 9;
		background: var(--bg-canvas);
		border-radius: var(--radius-sm);
		overflow: hidden;
		border: 2px solid rgba(220, 38, 38, 0.3);
		box-shadow: var(--shadow-inset);
		max-height: 100%;
		align-self: center;
	}

	.program-monitor canvas {
		width: 100%;
		height: 100%;
		display: block;
	}

	.monitor-label {
		position: absolute;
		top: 8px;
		left: 8px;
		padding: 2px 8px;
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		border-radius: var(--radius-sm);
		z-index: var(--z-above);
		text-transform: uppercase;
		background: var(--tally-program);
		color: var(--text-on-color);
	}

	.monitor-source {
		position: absolute;
		bottom: 8px;
		left: 8px;
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}

	.hidden-canvas {
		position: absolute;
		width: 0;
		height: 0;
		overflow: hidden;
		pointer-events: none;
	}

	/* ── Right Column ── */
	.right-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-base);
		min-height: 0;
	}

	.right-content {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.schedule-panel,
	.sources-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
	}

	.panel-header {
		padding: 10px 16px;
		font-size: var(--text-sm);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		text-transform: uppercase;
	}

	.panel-body {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 24px;
	}

	.placeholder-text {
		font-size: var(--text-sm);
		color: var(--text-tertiary);
		font-family: var(--font-mono);
	}

	/* ── Action Bar ── */
	.action-bar {
		padding: 10px 16px;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		display: flex;
		align-items: center;
		justify-content: center;
	}
</style>
