<script lang="ts">
	import { setASRBackendConfig, apiCall } from '$lib/api/switch-api';

	let { config = {} }: { config: Record<string, unknown> } = $props();

	let chunkMs = $derived((config.chunkMs as number) || 560);
	let language = $derived((config.language as string) || 'en');

	function update(key: string, value: unknown) {
		apiCall(setASRBackendConfig({ [key]: value }), `Set Nemotron ${key}`);
	}
</script>

<div class="backend-config">
	<div class="config-grid">
		<label class="config-grid__label" for="nemo-chunk">Chunk</label>
		<select
			id="nemo-chunk"
			class="field-select"
			value={String(chunkMs)}
			onchange={(e) => update('chunkMs', Number(e.currentTarget.value))}
		>
			<option value="80">80ms (lowest latency)</option>
			<option value="160">160ms</option>
			<option value="560">560ms (balanced)</option>
			<option value="1120">1120ms (highest accuracy)</option>
		</select>

		<label class="config-grid__label" for="nemo-lang">Language</label>
		<input
			id="nemo-lang"
			type="text"
			class="field-input"
			value={language}
			onchange={(e) => update('language', e.currentTarget.value)}
		/>

		<label class="config-grid__label" for="nemo-ep">Accel</label>
		<span class="status-text">CUDA EP</span>
	</div>
</div>

<style>
	.backend-config {
		display: flex;
		flex-direction: column;
		gap: 5px;
	}

	.config-grid {
		display: grid;
		grid-template-columns: minmax(0, auto) 1fr;
		gap: 3px 6px;
		align-items: center;
	}

	.config-grid__label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.field-select {
		width: 100%;
		min-width: 0;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		background: var(--bg-control);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		padding: 2px 4px;
		outline: none;
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}

	.field-select:focus {
		border-color: var(--accent-blue);
	}

	.field-input {
		width: 100%;
		min-width: 0;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		background: var(--bg-control);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		padding: 2px 4px;
		outline: none;
		box-sizing: border-box;
		transition: border-color var(--transition-fast);
	}

	.field-input:focus {
		border-color: var(--accent-blue);
	}

	.status-text {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}
</style>
