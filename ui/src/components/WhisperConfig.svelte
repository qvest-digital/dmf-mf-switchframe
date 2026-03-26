<script lang="ts">
	import { setASRBackendConfig, apiCall } from '$lib/api/switch-api';

	let { config = {} }: { config: Record<string, unknown> } = $props();

	let language = $derived((config.language as string) || 'auto');
	let translate = $derived((config.translate as boolean) ?? true);
	let speed = $derived((config.speed as string) || 'balanced');
	let noiseFloor = $derived((config.noiseFloor as string) || 'normal');

	function update(key: string, value: unknown) {
		apiCall(setASRBackendConfig({ [key]: value }), `Set Whisper ${key}`);
	}
</script>

<div class="backend-config">
	<div class="config-grid">
		<label class="config-grid__label" for="whisper-lang">Language</label>
		<select
			id="whisper-lang"
			class="field-select"
			value={language}
			onchange={(e) => update('language', e.currentTarget.value)}
		>
			<option value="auto">Auto-detect</option>
			<option value="en">English</option>
			<option value="es">Spanish</option>
			<option value="fr">French</option>
			<option value="de">German</option>
			<option value="zh">Chinese</option>
			<option value="ja">Japanese</option>
			<option value="ko">Korean</option>
			<option value="pt">Portuguese</option>
			<option value="ru">Russian</option>
			<option value="ar">Arabic</option>
			<option value="hi">Hindi</option>
			<option value="it">Italian</option>
		</select>

		<label class="config-grid__label" for="whisper-translate">Translate</label>
		<label class="translate-inline" for="whisper-translate">
			<input
				type="checkbox"
				id="whisper-translate"
				class="translate-inline__input"
				checked={translate}
				onchange={(e) => update('translate', e.currentTarget.checked)}
			/>
			<span class="translate-inline__text">to English</span>
		</label>
	</div>

	<div class="inline-tuning">
		<div class="inline-tuning__block">
			<span class="inline-tuning__label">Speed</span>
			<div class="segment-inline" role="group" aria-label="ASR speed">
				<button
					type="button"
					class="segment-inline__btn"
					class:segment-inline__btn--on={speed === 'fast'}
					onclick={() => update('speed', 'fast')}
				>Fast</button>
				<button
					type="button"
					class="segment-inline__btn"
					class:segment-inline__btn--on={speed === 'balanced'}
					onclick={() => update('speed', 'balanced')}
				>Bal</button>
				<button
					type="button"
					class="segment-inline__btn"
					class:segment-inline__btn--on={speed === 'accurate'}
					onclick={() => update('speed', 'accurate')}
				>Acc</button>
			</div>
		</div>
		<div class="inline-tuning__block">
			<span class="inline-tuning__label">Noise</span>
			<div class="segment-inline" role="group" aria-label="Noise floor">
				<button
					type="button"
					class="segment-inline__btn"
					class:segment-inline__btn--on={noiseFloor === 'quiet'}
					onclick={() => update('noiseFloor', 'quiet')}
				>Quiet</button>
				<button
					type="button"
					class="segment-inline__btn"
					class:segment-inline__btn--on={noiseFloor === 'normal'}
					onclick={() => update('noiseFloor', 'normal')}
				>Norm</button>
				<button
					type="button"
					class="segment-inline__btn"
					class:segment-inline__btn--on={noiseFloor === 'noisy'}
					onclick={() => update('noiseFloor', 'noisy')}
				>Noisy</button>
			</div>
		</div>
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

	.translate-inline {
		display: flex;
		align-items: center;
		gap: 5px;
		cursor: pointer;
		min-height: 20px;
	}

	.translate-inline__input {
		accent-color: var(--accent-blue);
		width: 12px;
		height: 12px;
		margin: 0;
		flex-shrink: 0;
	}

	.translate-inline__text {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
	}

	.inline-tuning {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 4px 6px;
		align-items: end;
	}

	.inline-tuning__block {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.inline-tuning__label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.02em;
	}

	.segment-inline {
		display: flex;
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-default);
		overflow: hidden;
		background: var(--bg-base);
	}

	.segment-inline__btn {
		flex: 1;
		min-width: 0;
		padding: 2px 2px;
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		border: none;
		border-right: 1px solid var(--border-subtle);
		background: var(--bg-control);
		color: var(--text-secondary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.segment-inline__btn:last-child {
		border-right: none;
	}

	.segment-inline__btn:hover:not(.segment-inline__btn--on) {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.segment-inline__btn--on {
		background: var(--accent-blue);
		color: var(--bg-base);
	}
</style>
