<script lang="ts">
	import { onMount } from 'svelte';
	import type { ControlRoomState, CaptionMode } from '$lib/api/types';
	import { setCaptionMode, sendCaptionText, sendCaptionNewline, clearCaptions, setASRConfig, getASRModels, apiCall } from '$lib/api/switch-api';
	import { ICON_CAPTION, ICON_MIC, ICON_INFO, renderIcon } from '$lib/icons';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	let textInput = $state('');

	let captionState = $derived(crState.captions);
	let mode = $derived(captionState?.mode ?? 'off');
	let authorBuffer = $derived(captionState?.authorBuffer ?? '');
	let sourceCaptions = $derived(captionState?.sourceCaptions ?? {});
	let isAuthor = $derived(mode === 'author');
	let isAuto = $derived((mode as string) === 'auto');
	let asrState = $derived(crState.asr);
	let asrAvailable = $derived(asrState?.available ?? false);
	let availableModels = $state<string[]>([]);

	// Fetch available models when ASR is available.
	onMount(() => {
		if (asrAvailable) {
			getASRModels().then((models) => { availableModels = models; }).catch(() => {});
		}
	});
	$effect(() => {
		if (asrAvailable && availableModels.length === 0) {
			getASRModels().then((models) => { availableModels = models; }).catch(() => {});
		}
	});

	const modes: { id: CaptionMode; label: string }[] = [
		{ id: 'off', label: 'Off' },
		{ id: 'passthrough', label: 'Pass-through' },
		{ id: 'author', label: 'Author' },
	];

	const languages = [
		{ code: 'auto', label: 'Auto-detect' },
		{ code: 'en', label: 'English' },
		{ code: 'es', label: 'Spanish' },
		{ code: 'fr', label: 'French' },
		{ code: 'de', label: 'German' },
		{ code: 'zh', label: 'Chinese' },
		{ code: 'ja', label: 'Japanese' },
		{ code: 'ko', label: 'Korean' },
		{ code: 'pt', label: 'Portuguese' },
		{ code: 'ru', label: 'Russian' },
		{ code: 'ar', label: 'Arabic' },
		{ code: 'hi', label: 'Hindi' },
		{ code: 'it', label: 'Italian' },
	];

	// -- Test Captions --
	const TEST_CAPTION_LINES = [
		'>> WELCOME TO THE BROADCAST.',
		'WE ARE COMING TO YOU LIVE',
		'FROM THE STUDIO.',
		'>> LET\'S CHECK IN WITH OUR',
		'REPORTER IN THE FIELD.',
		'>> THANKS. THE CONDITIONS HERE',
		'ARE PERFECT TODAY.',
		'TEMPERATURES IN THE MID 70S',
		'WITH CLEAR SKIES.',
		'>> COMING UP AFTER THE BREAK,',
		'WE\'LL HAVE THE LATEST',
		'ON THE CHAMPIONSHIP GAME.',
		'>> STAY WITH US.',
	];
	const TEST_LINE_DELAY = 2500;
	const TEST_LOOP_DELAY = 4000;

	let testCaptionsActive = $state(false);
	let testCaptionsTimer: ReturnType<typeof setTimeout> | null = null;

	function stopTestCaptions() {
		testCaptionsActive = false;
		if (testCaptionsTimer !== null) {
			clearTimeout(testCaptionsTimer);
			testCaptionsTimer = null;
		}
	}

	function startTestCaptions() {
		if (mode !== 'author') {
			apiCall(setCaptionMode('author'), 'Set caption mode');
		}
		testCaptionsActive = true;
		let index = 0;

		function feedNext() {
			if (!testCaptionsActive) return;
			const line = TEST_CAPTION_LINES[index];
			apiCall(sendCaptionText(line), 'Test caption text');
			apiCall(sendCaptionNewline(), 'Test caption newline');

			index++;
			if (index >= TEST_CAPTION_LINES.length) {
				index = 0;
				testCaptionsTimer = setTimeout(feedNext, TEST_LOOP_DELAY);
			} else {
				testCaptionsTimer = setTimeout(feedNext, TEST_LINE_DELAY);
			}
		}

		feedNext();
	}

	function toggleTestCaptions() {
		if (testCaptionsActive) {
			stopTestCaptions();
			apiCall(clearCaptions(), 'Clear test captions');
		} else {
			startTestCaptions();
		}
	}

	// Auto-stop test captions if mode changes away from author
	$effect(() => {
		if (mode !== 'author' && testCaptionsActive) {
			stopTestCaptions();
		}
	});

	// Cleanup on destroy
	$effect(() => {
		return () => {
			stopTestCaptions();
		};
	});

	function handleModeChange(newMode: CaptionMode | 'auto') {
		apiCall(setCaptionMode(newMode as CaptionMode), 'Set caption mode');
		// Auto mode enables ASR; leaving auto disables it.
		if (newMode === 'auto') {
			apiCall(setASRConfig({ active: true }), 'Enable ASR');
		} else if ((mode as string) === 'auto') {
			apiCall(setASRConfig({ active: false }), 'Disable ASR');
		}
	}

	function handleModelChange(e: Event) {
		const model = (e.target as HTMLSelectElement).value;
		apiCall(setASRConfig({ model }), 'Set ASR model');
	}

	function handleLanguageChange(e: Event) {
		const lang = (e.target as HTMLSelectElement).value;
		apiCall(setASRConfig({ language: lang }), 'Set ASR language');
	}

	function handleTranslateChange(e: Event) {
		const on = (e.target as HTMLInputElement).checked;
		apiCall(setASRConfig({ translate: on }), 'Set ASR translate');
	}

	let tuningOpen = $state(false);

	function handleSpeedChange(value: string) {
		apiCall(setASRConfig({ speed: value }), 'Set ASR speed');
	}

	function handleConfidenceChange(value: string) {
		apiCall(setASRConfig({ confidenceFilter: value }), 'Set ASR confidence filter');
	}

	function handleNoiseFloorChange(e: Event) {
		const value = (e.target as HTMLSelectElement).value;
		apiCall(setASRConfig({ noiseFloor: value }), 'Set ASR noise floor');
	}

	function handleSpeakerDetectionChange(e: Event) {
		const value = (e.target as HTMLSelectElement).value;
		apiCall(setASRConfig({ speakerDetection: value }), 'Set ASR speaker detection');
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			if (textInput.trim()) {
				apiCall(sendCaptionText(textInput), 'Send caption text');
				textInput = '';
			}
			apiCall(sendCaptionNewline(), 'Caption newline');
		}
	}

	function handleClear() {
		apiCall(clearCaptions(), 'Clear captions');
		textInput = '';
	}

	// Sources with captions detected.
	let captionSources = $derived(
		Object.entries(crState.sources ?? {})
			.filter(([key]) => sourceCaptions[key])
			.map(([key, info]) => ({ key, label: info.label || key }))
	);
</script>

<div class="captions-panel">
	<!-- Mode + settings -->
	<div class="zone zone--mode">
		<div class="zone-header">
			<span class="zone-header__title">
				<span class="zone-header__icon" aria-hidden="true">{@html renderIcon(ICON_CAPTION, 12)}</span>
				MODE
			</span>
			{#if isAuto}
				<span class="ai-badge">AI</span>
			{/if}
		</div>

		<div class="mode-segmented" role="group" aria-label="Caption mode">
			{#each modes as m}
				<button
					type="button"
					class="mode-segmented__btn"
					class:mode-segmented__btn--active={mode === m.id}
					title={m.label}
					onclick={() => handleModeChange(m.id)}
				>
					{m.label}
				</button>
			{/each}
			{#if asrAvailable}
				<button
					type="button"
					class="mode-segmented__btn mode-segmented__btn--auto"
					class:mode-segmented__btn--active-auto={isAuto}
					onclick={() => handleModeChange('auto')}
				>
					Auto
				</button>
			{/if}
		</div>

		{#if isAuto}
			<div class="zone-scroll">
				<div class="auto-grid">
					<label class="auto-grid__label" for="asr-translate">Translate</label>
					<label class="translate-inline" for="asr-translate">
						<input
							type="checkbox"
							id="asr-translate"
							class="translate-inline__input"
							checked={asrState?.translate ?? true}
							onchange={handleTranslateChange}
						/>
						<span class="translate-inline__text">to English</span>
					</label>

					<label class="auto-grid__label" for="asr-lang">Source</label>
					<select
						id="asr-lang"
						class="field-select"
						value={asrState?.language ?? 'auto'}
						onchange={handleLanguageChange}
					>
						{#each languages as lang}
							<option value={lang.code}>{lang.label}</option>
						{/each}
					</select>

					{#if availableModels.length > 0}
						<label class="auto-grid__label" for="asr-model">Model</label>
						<select
							id="asr-model"
							class="field-select field-select--mono"
							value={asrState?.modelName ?? ''}
							onchange={handleModelChange}
						>
							{#each availableModels as model}
								<option value={model}>{model}</option>
							{/each}
						</select>
					{/if}
				</div>

				<div class="inline-tuning">
					<div class="inline-tuning__block">
						<span class="inline-tuning__label">Speed</span>
						<div class="segment-inline" role="group" aria-label="ASR speed">
							<button
								type="button"
								class="segment-inline__btn"
								class:segment-inline__btn--on={(asrState?.speed ?? 'balanced') === 'fast'}
								onclick={() => handleSpeedChange('fast')}
							>Fast</button>
							<button
								type="button"
								class="segment-inline__btn"
								class:segment-inline__btn--on={(asrState?.speed ?? 'balanced') === 'balanced'}
								onclick={() => handleSpeedChange('balanced')}
							>Bal</button>
							<button
								type="button"
								class="segment-inline__btn"
								class:segment-inline__btn--on={(asrState?.speed ?? 'balanced') === 'accurate'}
								onclick={() => handleSpeedChange('accurate')}
							>Acc</button>
						</div>
					</div>
					<div class="inline-tuning__block">
						<span class="inline-tuning__label">Conf</span>
						<div class="segment-inline" role="group" aria-label="Confidence filter">
							<button
								type="button"
								class="segment-inline__btn"
								class:segment-inline__btn--on={(asrState?.confidenceFilter ?? 'medium') === 'low'}
								onclick={() => handleConfidenceChange('low')}
							>More</button>
							<button
								type="button"
								class="segment-inline__btn"
								class:segment-inline__btn--on={(asrState?.confidenceFilter ?? 'medium') === 'medium'}
								onclick={() => handleConfidenceChange('medium')}
							>Bal</button>
							<button
								type="button"
								class="segment-inline__btn"
								class:segment-inline__btn--on={(asrState?.confidenceFilter ?? 'medium') === 'high'}
								onclick={() => handleConfidenceChange('high')}
							>Strict</button>
						</div>
					</div>
				</div>

				<button type="button" class="tuning-toggle" class:tuning-toggle--open={tuningOpen} onclick={() => tuningOpen = !tuningOpen}>
					<span class="tuning-toggle__chev">{tuningOpen ? '▲' : '▼'}</span>
					Advanced
				</button>
				{#if tuningOpen}
					<div class="tuning-advanced">
						<div class="tuning-advanced__row">
							<span class="tuning-advanced__label">Noise</span>
							<select
								class="field-select"
								value={asrState?.noiseFloor ?? 'normal'}
								onchange={handleNoiseFloorChange}
							>
								<option value="quiet">Quiet studio</option>
								<option value="normal">Normal</option>
								<option value="noisy">Noisy venue</option>
							</select>
						</div>
						<div class="tuning-advanced__row">
							<span class="tuning-advanced__label">Spkr</span>
							<select
								class="field-select"
								value={asrState?.speakerDetection ?? 'normal'}
								onchange={handleSpeakerDetectionChange}
							>
								<option value="sensitive">Sensitive (200ms)</option>
								<option value="normal">Normal (300ms)</option>
								<option value="relaxed">Relaxed (500ms)</option>
							</select>
						</div>
					</div>
				{/if}
			</div>
		{/if}
	</div>

	<!-- Author input -->
	<div class="zone zone--author">
		<div class="zone-header">
			<span class="zone-header__title">
				<span class="zone-header__icon" aria-hidden="true">{@html renderIcon(ICON_MIC, 12)}</span>
				AUTHOR
			</span>
			<div class="zone-actions">
				<button
					type="button"
					class="chip-btn"
					class:chip-btn--active={testCaptionsActive}
					onclick={toggleTestCaptions}
				>
					{testCaptionsActive ? 'Stop test' : 'Test'}
				</button>
				{#if isAuthor}
					<button type="button" class="chip-btn chip-btn--danger" onclick={handleClear}>Clear</button>
				{/if}
			</div>
		</div>

		<div class="author-body">
			{#if isAuthor}
				{#if authorBuffer}
					<div class="author-buffer">
						<span class="author-buffer__label">On air</span>
						<span class="author-buffer__text">{authorBuffer}</span>
					</div>
				{/if}
				<textarea
					class="caption-input"
					placeholder="Type captions. Enter = send line + newline."
					bind:value={textInput}
					onkeydown={handleKeydown}
				></textarea>
			{:else}
				<div class="disabled-notice">
					<span class="disabled-notice__icon" aria-hidden="true">{@html renderIcon(ICON_INFO, 14)}</span>
					<div class="disabled-notice__text">
						{#if mode === 'auto'}
							<p>Auto captions from program audio.</p>
							{#if asrState?.tentative}
								<div class="tentative-text">{asrState.tentative}</div>
							{/if}
						{:else if mode === 'passthrough'}
							<p>Captions follow the program source.</p>
						{:else}
							<p>Enable Author mode to type live captions.</p>
						{/if}
					</div>
				</div>
			{/if}
		</div>
	</div>

	<!-- Source detection -->
	<div class="zone zone--sources">
		<div class="zone-header">
			<span class="zone-header__title">
				<span class="zone-header__icon" aria-hidden="true">{@html renderIcon(ICON_INFO, 12)}</span>
				SOURCES
			</span>
		</div>
		<div class="source-scroll">
			{#if captionSources.length === 0}
				<div class="source-empty">No embedded caption tracks detected.</div>
			{:else}
				<ul class="source-list">
					{#each captionSources as src}
						<li class="source-row">
							<span class="source-row__dot" aria-hidden="true"></span>
							<span class="source-row__name">{src.label}</span>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	</div>
</div>

<style>
	.captions-panel {
		display: grid;
		grid-template-columns: 1fr 2fr 1fr;
		gap: 6px;
		padding: 6px;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		box-sizing: border-box;
	}

	.zone {
		display: flex;
		flex-direction: column;
		min-height: 0;
		gap: 4px;
		padding: 5px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}

	.zone-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-shrink: 0;
		gap: 6px;
		min-height: 22px;
	}

	.zone-header__title {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
	}

	.zone-header__icon {
		display: inline-flex;
		color: var(--text-tertiary);
		opacity: 0.9;
	}

	.zone-header__icon :global(svg) {
		display: block;
	}

	.ai-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 700;
		background: var(--accent-blue);
		color: var(--bg-base);
		padding: 1px 5px;
		border-radius: var(--radius-xs);
		letter-spacing: 0.04em;
	}

	/* Segmented mode control */
	.mode-segmented {
		display: flex;
		flex-shrink: 0;
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-default);
		overflow: hidden;
		background: var(--bg-control);
	}

	.mode-segmented__btn {
		flex: 1;
		min-width: 0;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		line-height: 1.15;
		padding: 3px 3px;
		border: none;
		border-right: 1px solid var(--border-subtle);
		background: transparent;
		color: var(--text-secondary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.mode-segmented__btn:last-child {
		border-right: none;
	}

	.mode-segmented__btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.mode-segmented__btn--active {
		background: var(--accent-yellow);
		color: var(--bg-base);
	}

	.mode-segmented__btn--active:hover {
		background: var(--accent-yellow);
		color: var(--bg-base);
	}

	.mode-segmented__btn--auto.mode-segmented__btn--active-auto {
		background: var(--accent-blue);
		color: var(--bg-base);
	}

	.mode-segmented__btn--auto:hover:not(.mode-segmented__btn--active-auto) {
		color: var(--accent-blue);
	}

	.zone-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		display: flex;
		flex-direction: column;
		gap: 5px;
		padding-top: 2px;
	}

	.auto-grid {
		display: grid;
		grid-template-columns: minmax(0, auto) 1fr;
		gap: 3px 6px;
		align-items: center;
	}

	.auto-grid__label {
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

	.field-select--mono {
		font-family: var(--font-mono);
		font-size: 10px;
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

	.tuning-toggle {
		display: flex;
		align-items: center;
		gap: 4px;
		width: 100%;
		justify-content: center;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		padding: 3px 6px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		background: var(--bg-surface);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: border-color var(--transition-fast), color var(--transition-fast);
	}

	.tuning-toggle:hover {
		border-color: var(--border-default);
		color: var(--text-secondary);
	}

	.tuning-toggle--open {
		border-color: var(--accent-blue);
		color: var(--accent-blue);
	}

	.tuning-toggle__chev {
		font-size: 8px;
		opacity: 0.8;
	}

	.tuning-advanced {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 5px;
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
	}

	.tuning-advanced__row {
		display: grid;
		grid-template-columns: 36px 1fr;
		gap: 4px;
		align-items: center;
	}

	.tuning-advanced__label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
	}

	.author-body {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
		overflow: hidden;
	}

	.author-buffer {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		line-height: 1.35;
		color: var(--text-secondary);
		padding: 3px 5px;
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		max-height: 44px;
		overflow-y: auto;
		word-break: break-word;
		white-space: pre-wrap;
	}

	.author-buffer__label {
		display: block;
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		margin-bottom: 2px;
	}

	.author-buffer__text {
		color: var(--color-success);
	}

	.caption-input {
		flex: 1;
		min-height: 0;
		width: 100%;
		box-sizing: border-box;
		resize: none;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.4;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		padding: 5px 6px;
		outline: none;
		transition: border-color var(--transition-fast);
	}

	.caption-input:focus {
		border-color: var(--accent-yellow);
	}

	.caption-input::placeholder {
		color: var(--text-tertiary);
		font-size: var(--text-2xs);
	}

	.zone-actions {
		display: flex;
		gap: 3px;
		flex-shrink: 0;
	}

	.chip-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		padding: 2px 6px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		background: var(--bg-control);
		color: var(--text-secondary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
		white-space: nowrap;
	}

	.chip-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
		border-color: var(--border-default);
	}

	.chip-btn--active {
		background: var(--accent-yellow);
		color: var(--bg-base);
		border-color: var(--accent-yellow);
	}

	.chip-btn--danger:hover {
		background: var(--color-error-dim);
		color: var(--color-error);
		border-color: var(--color-error);
	}

	.disabled-notice {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		display: flex;
		gap: 6px;
		align-items: flex-start;
		padding: 6px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		line-height: 1.4;
	}

	.disabled-notice__icon {
		flex-shrink: 0;
		color: var(--text-tertiary);
		opacity: 0.75;
		margin-top: 1px;
	}

	.disabled-notice__icon :global(svg) {
		display: block;
	}

	.disabled-notice__text {
		min-width: 0;
	}

	.disabled-notice__text p {
		margin: 0 0 4px;
	}

	.disabled-notice__text p:last-child {
		margin-bottom: 0;
	}

	.tentative-text {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--accent-blue);
		opacity: 0.9;
		font-style: italic;
		word-break: break-word;
	}

	.source-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.source-empty {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		line-height: 1.35;
		padding: 4px 2px;
	}

	.source-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.source-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 2px 4px;
		border-radius: var(--radius-xs);
		background: var(--bg-base);
		border: 1px solid transparent;
	}

	.source-row__dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
		background: var(--color-success);
		box-shadow: 0 0 0 1px var(--color-success-dim);
	}

	.source-row__name {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-primary);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	@media (max-width: 1024px) {
		.captions-panel {
			grid-template-columns: 1fr 1fr;
		}

		.zone--sources {
			grid-column: span 2;
		}
	}

	@media (max-width: 768px) {
		.captions-panel {
			grid-template-columns: 1fr;
		}

		.zone--sources {
			grid-column: span 1;
		}
	}
</style>
