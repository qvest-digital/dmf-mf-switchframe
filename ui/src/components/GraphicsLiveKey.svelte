<script lang="ts">
	import type { GraphicsLayerState, SourceInfo } from '$lib/api/types';
	import { graphicsSetLive, graphicsClearLive, apiCall } from '$lib/api/switch-api';

	interface Props {
		layer: GraphicsLayerState;
		sources: Record<string, SourceInfo>;
	}

	let { layer, sources }: Props = $props();

	// Edits start from what the layer shows and are sent with SET. They are
	// reset only when another layer is shown or its live setup changes, not
	// on every state broadcast.
	let fill = $state('');
	let key = $state('');
	let premultiplied = $state(false);
	let shown = $derived(JSON.stringify([layer.id, layer.liveFill ?? '', layer.liveKey ?? '', layer.premultiplied ?? false]));
	$effect(() => {
		[, fill, key, premultiplied] = JSON.parse(shown);
	});

	let sourceKeys = $derived(Object.keys(sources).sort());
	let canSet = $derived(fill !== '' && key !== '' && fill !== key);

	function set() {
		apiCall(graphicsSetLive(layer.id, { fill, key, premultiplied }), 'Live key');
	}

	function clear() {
		apiCall(graphicsClearLive(layer.id), 'Live key');
	}
</script>

<div class="live-key">
	<label class="live-row">
		<span class="live-label">FILL</span>
		<select class="field-inp live-fill" bind:value={fill}>
			<option value="">--</option>
			{#each sourceKeys as k (k)}
				<option value={k}>{sources[k]?.label || k}</option>
			{/each}
		</select>
	</label>
	<label class="live-row">
		<span class="live-label">KEY</span>
		<select class="field-inp live-key-src" bind:value={key}>
			<option value="">--</option>
			{#each sourceKeys as k (k)}
				<option value={k}>{sources[k]?.label || k}</option>
			{/each}
		</select>
	</label>
	<label class="live-row live-check">
		<input type="checkbox" class="live-premultiplied" bind:checked={premultiplied} />
		<span>Premultiplied fill</span>
	</label>
	<div class="live-row">
		<button class="act-btn upload live-set" onclick={set} disabled={!canSet}>SET</button>
		<button class="act-btn del live-clear" onclick={clear} disabled={!layer.liveFill}>CLEAR</button>
	</div>
	<p class="help-text">The key's luma is the fill's alpha. Full frame, frame-aligned.</p>
</div>

<style>
	.live-key {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.live-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.live-label {
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-secondary);
		width: 28px;
		flex-shrink: 0;
	}

	.live-check {
		font-size: var(--text-xs);
		color: var(--text-primary);
	}

	.field-inp {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 5px;
		width: 100%;
	}

	.act-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.03em;
		padding: 3px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
	}

	.act-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.act-btn.upload:not(:disabled):hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.act-btn.del:not(:disabled):hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
	}

	.help-text {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		line-height: 1.4;
		color: var(--text-tertiary);
		margin: 0;
	}
</style>
