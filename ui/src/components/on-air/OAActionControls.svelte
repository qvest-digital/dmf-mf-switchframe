<script lang="ts">
	interface Props {
		mode: 'playout' | 'live-monitor';
		bugActive: boolean;
		captionMode: 'off' | 'passthrough' | 'author';
		podArmed: boolean;
		inBreak: boolean;
		holdActive: boolean;
		transitionType?: string;
		transitionDuration?: number;
		onTransitionTypeChange?: (type: string) => void;
		onTransitionDurationChange?: (ms: number) => void;
		onFirePod?: () => void;
		onReturnToLive?: () => void;
		onBugToggle?: () => void;
		onHold?: () => void;
		onSkip?: () => void;
		onTakeNext?: () => void;
		onCaptionCycle?: () => void;
		onSlate?: () => void;
		onCut?: () => void;
		onAuto?: () => void;
		onScteInsert?: () => void;
		onScteReturn?: () => void;
		onAudioToggle?: () => void;
		onIOToggle?: () => void;
		onSettingsOpen?: () => void;
		onGraphicsToggle?: () => void;
		onScteToggle?: () => void;
		onCaptionsToggle?: () => void;
		onReplayToggle?: () => void;
		onClipsToggle?: () => void;
		onMacrosToggle?: () => void;
		onCommsToggle?: () => void;
	}

	let {
		mode,
		bugActive,
		captionMode,
		podArmed,
		inBreak,
		holdActive,
		transitionType = 'mix',
		transitionDuration = 1000,
		onTransitionTypeChange,
		onTransitionDurationChange,
		onFirePod,
		onReturnToLive,
		onBugToggle,
		onHold,
		onSkip,
		onTakeNext,
		onCaptionCycle,
		onSlate,
		onCut,
		onAuto,
		onScteInsert,
		onScteReturn,
		onAudioToggle,
		onIOToggle,
		onSettingsOpen,
		onGraphicsToggle,
		onScteToggle,
		onCaptionsToggle,
		onReplayToggle,
		onClipsToggle,
		onMacrosToggle,
		onCommsToggle,
	}: Props = $props();

	const captionLabel = $derived.by((): string => {
		const map: Record<string, string> = {
			off: 'OFF',
			passthrough: 'PASS',
			author: 'AUTHOR',
		};
		return map[captionMode] ?? 'OFF';
	});

	function handleFirePod() {
		if (podArmed && onFirePod) onFirePod();
	}
</script>

<div class="oa-action-controls" role="toolbar" aria-label="Action controls">
	<div class="actions-left">
		{#if mode === 'playout'}
			<!-- Playout primary row -->
			<div class="action-row primary-row">
				<button
					class="btn btn-primary fire-pod-btn"
					class:pod-armed={podArmed}
					class:pod-disabled={!podArmed}
					onclick={handleFirePod}
					aria-disabled={!podArmed}
				>
					FIRE POD
				</button>
				{#if inBreak}
					<button class="btn btn-primary return-btn" onclick={onReturnToLive}>
						RETURN TO LIVE
					</button>
				{/if}
			</div>
			<!-- Playout secondary row -->
			<div class="action-row secondary-row">
				<button
					class="btn btn-secondary hold-btn"
					class:hold-active={holdActive}
					onclick={onHold}
				>
					HOLD
				</button>
				<button class="btn btn-secondary" onclick={onSkip}>SKIP</button>
				<button class="btn btn-secondary" onclick={onTakeNext}>TAKE NEXT</button>
			</div>
		{:else}
			<!-- Live-monitor primary row -->
			<div class="action-row primary-row">
				<button class="btn btn-primary cut-btn" onclick={onCut}>CUT</button>
				<button class="btn btn-primary auto-btn" onclick={onAuto}>AUTO</button>
				<select
					class="transition-select"
					value={transitionType}
					onchange={(e) => onTransitionTypeChange?.(e.currentTarget.value)}
				>
					<option value="mix">Mix</option>
					<option value="dip">Dip</option>
					<option value="wipe">Wipe</option>
				</select>
				<input
					type="number"
					class="transition-duration"
					value={transitionDuration}
					min="100"
					max="5000"
					step="100"
					title="Transition duration (ms)"
					onchange={(e) => onTransitionDurationChange?.(parseInt(e.currentTarget.value))}
				/>
			</div>
			<!-- Live-monitor secondary row -->
			<div class="action-row secondary-row">
				<button class="btn btn-secondary" onclick={onScteInsert}>SCTE INSERT</button>
				<button class="btn btn-secondary" onclick={onScteReturn}>SCTE RETURN</button>
			</div>
		{/if}
	</div>

	<div class="actions-right">
		<!-- Bug + captions -->
		<div class="action-row right-top">
			<button
				class="btn btn-secondary bug-btn"
				class:bug-active={bugActive}
				onclick={onBugToggle}
			>
				{bugActive ? 'BUG: ON' : 'BUG: OFF'}
			</button>
			{#if mode === 'playout'}
				<button class="btn btn-secondary" onclick={onSlate}>SLATE</button>
			{/if}
			<button class="btn btn-secondary caption-btn" onclick={onCaptionCycle}>
				CAPTIONS: {captionLabel}
			</button>
		</div>

		<!-- Utility row -->
		<div class="action-row utility-row">
			<button class="btn btn-utility" onclick={onAudioToggle} title="Toggle audio panel">
				AUDIO
			</button>
			<button class="btn btn-utility" onclick={onIOToggle} title="Toggle I/O panel">
				I/O
			</button>
			<button class="btn btn-utility" onclick={onCommsToggle} title="Toggle comms panel">
				COMMS
			</button>
			<button class="btn btn-utility" onclick={onSettingsOpen} title="Open settings">
				SETTINGS
			</button>
		</div>
		<!-- Panels row -->
		<div class="action-row panels-row">
			<button class="btn btn-panel" onclick={onGraphicsToggle} title="Toggle graphics/compose panel">
				GFX
			</button>
			<button class="btn btn-panel" onclick={onScteToggle} title="Toggle SCTE-35 panel">
				SCTE
			</button>
			<button class="btn btn-panel" onclick={onCaptionsToggle} title="Toggle captions panel">
				CC
			</button>
			<button class="btn btn-panel" onclick={onReplayToggle} title="Toggle replay panel">
				REPLAY
			</button>
			<button class="btn btn-panel" onclick={onClipsToggle} title="Toggle clips panel">
				CLIPS
			</button>
			<button class="btn btn-panel" onclick={onMacrosToggle} title="Toggle macros panel">
				MACRO
			</button>
		</div>
	</div>
</div>

<style>
	.oa-action-controls {
		display: flex;
		gap: 12px;
		background: var(--bg-surface);
		border-top: 1px solid var(--border-default);
		padding: 8px 12px;
		user-select: none;
	}

	.actions-left {
		display: flex;
		flex-direction: column;
		gap: 6px;
		flex: 1 1 auto;
	}

	.actions-right {
		display: flex;
		flex-direction: column;
		gap: 6px;
		align-items: flex-end;
		flex: 0 1 auto;
	}

	.action-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.right-top {
		justify-content: flex-end;
	}

	.utility-row {
		justify-content: flex-end;
		margin-top: auto;
	}

	/* ── Base button ── */
	.btn {
		cursor: pointer;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-control);
		color: var(--text-primary);
		white-space: nowrap;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast),
			color var(--transition-fast),
			box-shadow var(--transition-fast);
	}

	.btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	/* ── Primary buttons ── */
	.btn-primary {
		font-family: var(--font-ui);
		font-size: 13px;
		font-weight: 700;
		letter-spacing: 0.03em;
		padding: 8px 16px;
	}

	/* ── Secondary buttons ── */
	.btn-secondary {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: 4px 10px;
	}

	/* ── Utility buttons ── */
	.btn-utility {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: 4px 10px;
		color: var(--text-secondary);
	}

	.btn-utility:hover {
		color: var(--text-primary);
	}

	/* ── FIRE POD states ── */
	.fire-pod-btn.pod-armed {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.4);
		color: var(--accent-yellow);
	}

	.fire-pod-btn.pod-armed:hover {
		background: rgba(234, 179, 8, 0.2);
		border-color: rgba(234, 179, 8, 0.6);
	}

	.fire-pod-btn.pod-disabled {
		background: var(--bg-control);
		color: var(--text-secondary);
		opacity: 0.5;
		cursor: not-allowed;
	}

	.fire-pod-btn.pod-disabled:hover {
		background: var(--bg-control);
		border-color: var(--border-default);
	}

	/* ── RETURN TO LIVE ── */
	.return-btn {
		background: var(--tally-program-dim);
		border-color: rgba(220, 38, 38, 0.4);
		color: var(--tally-program);
	}

	.return-btn:hover {
		background: rgba(220, 38, 38, 0.2);
		border-color: rgba(220, 38, 38, 0.6);
	}

	/* ── CUT / AUTO ── */
	.cut-btn {
		background: var(--tally-program-dim);
		border-color: rgba(220, 38, 38, 0.4);
		color: var(--tally-program);
	}

	.cut-btn:hover {
		background: rgba(220, 38, 38, 0.2);
		border-color: rgba(220, 38, 38, 0.6);
	}

	.auto-btn {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.4);
		color: var(--accent-yellow);
	}

	.auto-btn:hover {
		background: rgba(234, 179, 8, 0.2);
		border-color: rgba(234, 179, 8, 0.6);
	}

	/* ── BUG toggle ── */
	.bug-btn.bug-active {
		background: var(--color-success-dim);
		border-color: rgba(34, 197, 94, 0.4);
		color: var(--color-success);
	}

	.bug-btn.bug-active:hover {
		background: rgba(34, 197, 94, 0.2);
		border-color: rgba(34, 197, 94, 0.6);
	}

	/* ── HOLD toggle ── */
	.hold-btn.hold-active {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.4);
		color: var(--accent-yellow);
	}

	.hold-btn.hold-active:hover {
		background: rgba(234, 179, 8, 0.2);
		border-color: rgba(234, 179, 8, 0.6);
	}

	/* ── Panel buttons row ── */
	.panels-row {
		justify-content: flex-end;
		flex-wrap: wrap;
	}

	.btn-panel {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.05em;
		padding: 3px 8px;
		color: var(--text-tertiary);
		border-color: var(--border-subtle);
	}

	.btn-panel:hover {
		color: var(--text-primary);
		border-color: var(--border-default);
	}

	/* ── Transition selector ── */
	.transition-select {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.03em;
		padding: 4px 6px;
		background: var(--bg-control);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}

	.transition-select:hover {
		border-color: var(--border-strong);
	}

	.transition-duration {
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 600;
		padding: 4px 6px;
		width: 60px;
		background: var(--bg-control);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		transition: border-color var(--transition-fast);
	}

	.transition-duration:hover,
	.transition-duration:focus {
		border-color: var(--border-strong);
		outline: none;
	}
</style>
