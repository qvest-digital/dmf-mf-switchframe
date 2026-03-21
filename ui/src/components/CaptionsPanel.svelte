<script lang="ts">
	import type { ControlRoomState, CaptionMode } from '$lib/api/types';
	import { setCaptionMode, sendCaptionText, sendCaptionNewline, clearCaptions, setASRConfig, apiCall } from '$lib/api/switch-api';

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
	let isAuto = $derived(mode === 'auto');
	let asrState = $derived(crState.asr);
	let asrAvailable = $derived(asrState?.available ?? false);

	const modes: { id: CaptionMode; label: string }[] = [
		{ id: 'off', label: 'Off' },
		{ id: 'passthrough', label: 'Pass-through' },
		{ id: 'author', label: 'Author' },
	];

	const languages = [
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

	function handleModeChange(newMode: CaptionMode) {
		apiCall(setCaptionMode(newMode), 'Set caption mode');
		if (newMode === 'auto') {
			apiCall(setASRConfig({ active: true }), 'Enable ASR');
		}
	}

	function handleLanguageChange(e: Event) {
		const lang = (e.target as HTMLSelectElement).value;
		apiCall(setASRConfig({ language: lang }), 'Set ASR language');
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
	<!-- Mode bar -->
	<div class="zone">
		<div class="zone-header">
			<span class="zone-title">MODE</span>
			{#if isAuto}
				<span class="ai-badge">AI</span>
			{/if}
		</div>
		<div class="mode-bar">
			{#each modes as m}
				<button
					class="mode-btn"
					class:active={mode === m.id}
					onclick={() => handleModeChange(m.id)}
				>
					{m.label}
				</button>
			{/each}
			{#if asrAvailable}
				<button
					class="mode-btn auto-btn"
					class:active={isAuto}
					onclick={() => handleModeChange('auto')}
				>
					Auto
				</button>
			{/if}
		</div>
		{#if isAuto}
			<div class="language-row">
				<label class="language-label" for="asr-lang">Language</label>
				<select
					id="asr-lang"
					class="language-select"
					value={asrState?.language ?? 'en'}
					onchange={handleLanguageChange}
				>
					{#each languages as lang}
						<option value={lang.code}>{lang.label}</option>
					{/each}
				</select>
				{#if asrState?.modelName}
					<span class="model-name">{asrState.modelName}</span>
				{/if}
			</div>
		{/if}
	</div>

	<!-- Author input -->
	<div class="zone">
		<div class="zone-header">
			<span class="zone-title">AUTHOR INPUT</span>
			<div class="zone-actions">
				<button
					class="test-btn"
					class:active={testCaptionsActive}
					onclick={toggleTestCaptions}
				>
					{testCaptionsActive ? 'Stop Test' : 'Test Captions'}
				</button>
				{#if isAuthor}
					<button class="clear-btn" onclick={handleClear}>Clear Display</button>
				{/if}
			</div>
		</div>
		{#if isAuthor}
			<textarea
				class="caption-input"
				placeholder="Type captions here. Press Enter to send + new line."
				bind:value={textInput}
				onkeydown={handleKeydown}
			></textarea>
			{#if authorBuffer}
				<div class="author-buffer">
					<span class="buffer-label">On screen:</span>
					<span class="buffer-text">{authorBuffer}</span>
				</div>
			{/if}
		{:else}
			<div class="disabled-notice">
				{#if mode === 'auto'}
					Captions generated automatically from program audio.
					{#if asrState?.tentative}
						<div class="tentative-text">{asrState.tentative}</div>
					{/if}
				{:else if mode === 'passthrough'}
					Captions forwarded from program source.
				{:else}
					Enable Author mode to type live captions.
				{/if}
			</div>
		{/if}
	</div>

	<!-- Source detection -->
	<div class="zone">
		<div class="zone-header">
			<span class="zone-title">SOURCE DETECTION</span>
		</div>
		<div class="source-list">
			{#if captionSources.length === 0}
				<div class="no-sources">No sources with embedded captions detected.</div>
			{:else}
				{#each captionSources as src}
					<div class="source-badge">
						<span class="cc-badge">CC</span>
						<span class="source-name">{src.label}</span>
					</div>
				{/each}
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
		overflow: hidden;
	}

	.zone {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px;
		background: var(--bg-elevated);
		border-radius: var(--radius-sm);
		overflow-y: auto;
	}

	.zone-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.zone-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	.mode-bar {
		display: flex;
		gap: 4px;
	}

	.mode-btn {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		padding: 6px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		transition: all var(--transition-fast);
	}

	.mode-btn:hover {
		background: var(--bg-elevated);
		color: var(--text-primary);
	}

	.mode-btn.active {
		background: var(--accent-yellow);
		color: var(--bg-base);
		border-color: var(--accent-yellow);
	}

	.auto-btn.active {
		background: #a855f7;
		border-color: #a855f7;
		color: white;
	}

	.auto-btn:not(.active):hover {
		border-color: #a855f7;
		color: #a855f7;
	}

	.ai-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 700;
		background: #a855f7;
		color: white;
		padding: 1px 6px;
		border-radius: 3px;
		letter-spacing: 0.05em;
	}

	.language-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-top: 2px;
	}

	.language-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-secondary);
		white-space: nowrap;
	}

	.language-select {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 3px 6px;
		outline: none;
		cursor: pointer;
	}

	.language-select:focus {
		border-color: #a855f7;
	}

	.model-name {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.tentative-text {
		margin-top: 6px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: #a855f7;
		opacity: 0.7;
		font-style: italic;
	}

	.caption-input {
		flex: 1;
		min-height: 60px;
		resize: none;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 8px;
		outline: none;
	}

	.caption-input:focus {
		border-color: var(--accent-yellow);
	}

	.caption-input::placeholder {
		color: var(--text-tertiary);
	}

	.author-buffer {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		padding: 4px 8px;
		background: var(--bg-base);
		border-radius: var(--radius-sm);
		white-space: pre-wrap;
		word-break: break-word;
		max-height: 48px;
		overflow-y: auto;
	}

	.buffer-label {
		color: var(--text-tertiary);
		margin-right: 4px;
	}

	.buffer-text {
		color: var(--accent-green);
	}

	.zone-actions {
		display: flex;
		gap: 4px;
	}

	.test-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
	}

	.test-btn:hover {
		background: var(--bg-elevated);
		color: var(--text-primary);
	}

	.test-btn.active {
		background: var(--accent-yellow);
		color: var(--bg-base);
		border-color: var(--accent-yellow);
	}

	.clear-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
	}

	.clear-btn:hover {
		background: var(--status-error);
		color: white;
		border-color: var(--status-error);
	}

	.disabled-notice {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		padding: 12px;
		text-align: center;
	}

	.source-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.no-sources {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		padding: 8px;
	}

	.source-badge {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		background: var(--bg-base);
		border-radius: var(--radius-sm);
	}

	.cc-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 700;
		background: var(--accent-yellow);
		color: var(--bg-base);
		padding: 1px 4px;
		border-radius: 2px;
	}

	.source-name {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-primary);
	}

	@media (max-width: 1024px) {
		.captions-panel {
			grid-template-columns: 1fr 1fr;
		}
		.zone:last-child {
			grid-column: span 2;
		}
	}

	@media (max-width: 768px) {
		.captions-panel {
			grid-template-columns: 1fr;
		}
		.zone:last-child {
			grid-column: span 1;
		}
	}
</style>
