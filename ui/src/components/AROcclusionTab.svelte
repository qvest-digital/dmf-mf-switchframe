<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import {
		arOcclusionEnable, arOcclusionDisable,
		arOcclusionAddPrompt, arOcclusionDeletePrompt,
		apiCall,
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import { onDestroy } from 'svelte';
	import { getArPromptState, startPromptMode, stopPromptMode, setPromptMarkers } from '$lib/state/ar-prompt.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	const occlusion = $derived(crState.arOcclusion);
	const enabled = $derived(occlusion?.enabled ?? false);
	const depthReady = $derived(occlusion?.depthReady ?? false);
	const samReady = $derived(occlusion?.samReady ?? false);
	const depthLatency = $derived(occlusion?.depthLatencyMs ?? 0);
	const samLatency = $derived(occlusion?.samLatencyMs ?? 0);
	const prompts = $derived(occlusion?.prompts ?? []);
	const framesProcessed = $derived(occlusion?.framesProcessed ?? 0);
	const framesSkipped = $derived(occlusion?.framesSkipped ?? 0);

	const arPrompt = getArPromptState();
	const isPromptMode = $derived(arPrompt.mode !== 'off');

	onDestroy(() => {
		stopPromptMode();
	});

	let pendingLabel = $state('');
	let showLabelInput = $state(false);
	let pendingType = $state<string>('point');
	let pendingPoints = $state<number[][]>([]);

	function toggleOcclusion() {
		if (enabled) {
			apiCall(arOcclusionDisable(), 'Disable occlusion');
		} else {
			apiCall(arOcclusionEnable(), 'Enable occlusion');
		}
	}

	function handleAddPrompt() {
		if (isPromptMode) {
			// Cancel prompt mode
			stopPromptMode();
			setPromptMarkers([]);
			showLabelInput = false;
			return;
		}
		startPromptMode((type: string, points: number[][]) => {
			pendingType = type;
			pendingPoints = points;
			showLabelInput = true;
			stopPromptMode();
		});
	}

	function confirmPromptLabel() {
		const label = pendingLabel.trim() || 'Prompt';
		if (pendingType === 'box') {
			apiCall(
				arOcclusionAddPrompt({
					label,
					type: 'box',
					box: pendingPoints[0] as unknown as [number, number, number, number],
				}),
				'Add prompt',
			);
		} else {
			apiCall(
				arOcclusionAddPrompt({
					label,
					type: 'point',
					points: pendingPoints as unknown as [number, number][],
				}),
				'Add prompt',
			);
		}
		notify('info', `Adding prompt "${label}"`);
		pendingLabel = '';
		showLabelInput = false;
		setPromptMarkers([]);
	}

	function cancelLabel() {
		showLabelInput = false;
		pendingLabel = '';
		setPromptMarkers([]);
	}

	function handleDeletePrompt(promptId: string) {
		apiCall(arOcclusionDeletePrompt(promptId), 'Delete prompt');
	}

	function typeBadgeClass(type: string): string {
		switch (type) {
			case 'point': return 'badge-point';
			case 'box': return 'badge-box';
			default: return 'badge-default';
		}
	}
</script>

<div class="occlusion-tab">
	<!-- Model toggles row -->
	<div class="model-row">
		<div class="model-item">
			<span class="model-label">DEPTH</span>
			<button
				class="toggle-btn"
				class:active={depthReady}
				onclick={toggleOcclusion}
			>{enabled ? 'ON' : 'OFF'}</button>
			{#if depthReady}
				<span class="latency">{depthLatency.toFixed(1)}ms</span>
			{/if}
		</div>
		<div class="model-item">
			<span class="model-label">SAM</span>
			<span class="toggle-btn" class:active={samReady}>{samReady ? 'ON' : 'OFF'}</span>
			{#if samReady}
				<span class="latency">{samLatency.toFixed(1)}ms</span>
			{/if}
		</div>
	</div>

	<!-- SAM Prompts -->
	<div class="section">
		<div class="section-header">
			<span class="section-label">SAM PROMPTS</span>
			<button class="add-btn" class:cancel-mode={isPromptMode} onclick={handleAddPrompt}>
				{isPromptMode ? 'Cancel' : '+ Add Prompt'}
			</button>
		</div>
		{#if showLabelInput}
			<div class="label-input-row">
				<input
					class="label-input"
					type="text"
					placeholder="Prompt label..."
					bind:value={pendingLabel}
					onkeydown={(e) => { if (e.key === 'Enter') confirmPromptLabel(); if (e.key === 'Escape') cancelLabel(); }}
				/>
				<button class="label-confirm" onclick={confirmPromptLabel}>OK</button>
				<button class="label-cancel" onclick={cancelLabel}>&times;</button>
			</div>
		{/if}
		{#if prompts.length === 0 && !showLabelInput}
			<div class="empty-state">No prompts configured</div>
		{:else}
			<div class="prompt-list">
				{#each prompts as p (p.id)}
					<div class="prompt-row">
						<span class="prompt-dot"></span>
						<span class="prompt-label">{p.label}</span>
						<span class="prompt-type {typeBadgeClass(p.type)}">{p.type}</span>
						<button class="delete-btn" onclick={() => handleDeletePrompt(p.id)} title="Remove prompt">&times;</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Status row -->
	<div class="status-row">
		<span class="status-item">
			<span class="status-key">processed</span>
			<span class="status-val">{framesProcessed.toLocaleString()}</span>
		</span>
		<span class="status-item">
			<span class="status-key">skipped</span>
			<span class="status-val">{framesSkipped.toLocaleString()}</span>
		</span>
	</div>
</div>

<style>
	.occlusion-tab {
		padding: 8px 10px;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	/* Model toggles */
	.model-row {
		display: flex;
		gap: 16px;
	}

	.model-item {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.model-label {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.05em;
	}

	.toggle-btn {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		padding: 1px 6px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 2px;
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-tertiary, #666);
		cursor: pointer;
	}

	.toggle-btn.active {
		background: rgba(34, 197, 94, 0.15);
		border-color: rgba(34, 197, 94, 0.4);
		color: rgb(34, 197, 94);
	}

	.latency {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
	}

	/* Section */
	.section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.section-label {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.06em;
	}

	.add-btn {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 500;
		padding: 1px 6px;
		border: 1px solid rgba(99, 102, 241, 0.3);
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.1);
		color: var(--accent-indigo, #818cf8);
		cursor: pointer;
		transition: background 0.15s;
	}

	.add-btn:hover {
		background: rgba(99, 102, 241, 0.2);
	}

	.add-btn.cancel-mode {
		border-color: rgba(239, 68, 68, 0.3);
		background: rgba(239, 68, 68, 0.1);
		color: var(--color-error, #ef4444);
	}

	.add-btn.cancel-mode:hover {
		background: rgba(239, 68, 68, 0.2);
	}

	.label-input-row {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 0;
	}

	.label-input {
		flex: 1;
		font-family: var(--font-ui, sans-serif);
		font-size: 10px;
		padding: 2px 6px;
		border: 1px solid rgba(168, 85, 247, 0.3);
		border-radius: 2px;
		background: rgba(168, 85, 247, 0.06);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.label-input:focus {
		border-color: rgba(168, 85, 247, 0.6);
	}

	.label-confirm {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 600;
		padding: 2px 6px;
		border: 1px solid rgba(34, 197, 94, 0.3);
		border-radius: 2px;
		background: rgba(34, 197, 94, 0.1);
		color: rgb(34, 197, 94);
		cursor: pointer;
	}

	.label-confirm:hover {
		background: rgba(34, 197, 94, 0.2);
	}

	.label-cancel {
		font-size: 14px;
		line-height: 1;
		padding: 0 2px;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
	}

	.label-cancel:hover {
		color: var(--color-error, #ef4444);
	}

	.empty-state {
		font-size: 10px;
		color: var(--text-tertiary, #666);
		font-style: italic;
		padding: 4px 0;
	}

	.prompt-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.prompt-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 2px 4px;
		border-radius: 2px;
		background: rgba(255, 255, 255, 0.02);
	}

	.prompt-row:hover {
		background: rgba(255, 255, 255, 0.05);
	}

	.prompt-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--accent-indigo, #818cf8);
		flex-shrink: 0;
	}

	.prompt-label {
		font-size: 11px;
		color: var(--text-primary, #eee);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.prompt-type {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		font-weight: 600;
		padding: 0 4px;
		border-radius: 2px;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.badge-point {
		background: rgba(59, 130, 246, 0.15);
		color: rgb(96, 165, 250);
	}

	.badge-box {
		background: rgba(245, 158, 11, 0.15);
		color: rgb(251, 191, 36);
	}

	.badge-default {
		background: rgba(255, 255, 255, 0.06);
		color: var(--text-secondary, #999);
	}

	.delete-btn {
		font-size: 14px;
		line-height: 1;
		padding: 0 2px;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		opacity: 0;
		transition: opacity 0.15s, color 0.15s;
	}

	.prompt-row:hover .delete-btn {
		opacity: 1;
	}

	.delete-btn:hover {
		color: var(--color-error, #ef4444);
	}

	/* Status row */
	.status-row {
		display: flex;
		gap: 16px;
		padding-top: 4px;
		border-top: 1px solid rgba(255, 255, 255, 0.06);
	}

	.status-item {
		display: flex;
		gap: 4px;
		align-items: baseline;
	}

	.status-key {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-tertiary, #666);
	}

	.status-val {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
		font-weight: 600;
	}
</style>
