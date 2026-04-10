<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import PresetPanel from '../PresetPanel.svelte';
	import VideoProcessingPanel from '../VideoProcessingPanel.svelte';
	import TeamPanel from '../TeamPanel.svelte';
	import { setEncoderBackend, setFormat, getFormat, getEncoder } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		visible: boolean;
		onclose: () => void;
		crState: ControlRoomState;
		onOpenGraphics?: () => void;
	}

	let { visible, onclose, crState, onOpenGraphics }: Props = $props();

	let activeTab = $state<string>('presets');

	const tabs = ['presets', 'dve', 'keys', 'video', 'encoder', 'format', 'team'] as const;

	const tabLabels: Record<typeof tabs[number], string> = {
		presets: 'Presets',
		dve: 'DVE',
		keys: 'Keys',
		video: 'Video',
		encoder: 'Encoder',
		format: 'Format',
		team: 'Team',
	};

	// ── Encoder tab state ──
	let encoderChanging = $state(false);

	async function handleEncoderChange(name: string) {
		encoderChanging = true;
		try {
			await setEncoderBackend(name);
			notify('Encoder changed to ' + name, 'success');
		} catch {
			notify('Failed to change encoder', 'error');
		} finally {
			encoderChanging = false;
		}
	}

	// ── Format tab state ──
	let formatPresets = $state<string[]>([]);
	let formatLoaded = $state(false);

	async function loadFormatPresets() {
		if (formatLoaded) return;
		try {
			const result = await getFormat();
			formatPresets = result.presets ?? [];
			formatLoaded = true;
		} catch { /* ignore */ }
	}

	async function handleFormatChange(preset: string) {
		try {
			await setFormat(preset);
			notify('Format changed to ' + preset, 'success');
		} catch {
			notify('Failed to change format', 'error');
		}
	}

	// Load format presets when format tab is selected
	$effect(() => {
		if (activeTab === 'format' && visible) {
			loadFormatPresets();
		}
	});

	function handleBackdropClick(e: MouseEvent) {
		if (e.target === e.currentTarget) {
			onclose();
		}
	}

	function openGfxPanel() {
		onclose();
		onOpenGraphics?.();
	}
</script>

{#if visible}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="settings-backdrop" onclick={handleBackdropClick}>
		<div
			class="settings-modal"
			role="dialog"
			aria-modal="true"
			aria-label="Settings"
		>
			<header class="settings-header">
				<h2 class="settings-title">SETTINGS</h2>
				<button class="close-btn" onclick={onclose} title="Close settings">
					&times;
				</button>
			</header>

			<div class="tab-bar" role="tablist" aria-label="Settings tabs">
				{#each tabs as tab}
					<button
						class="tab"
						class:active={activeTab === tab}
						role="tab"
						aria-selected={activeTab === tab}
						onclick={() => { activeTab = tab; }}
					>
						{tabLabels[tab]}
					</button>
				{/each}
			</div>

			<div
				class="tab-content"
				role="tabpanel"
			>
				{#if activeTab === 'presets'}
					<div class="tab-panel-wrap">
						<PresetPanel />
					</div>
				{:else if activeTab === 'dve'}
					<div class="tab-redirect">
						<p>DVE controls are available in the GFX panel.</p>
						<button class="btn-open-gfx" onclick={openGfxPanel}>Open GFX Panel</button>
					</div>
				{:else if activeTab === 'keys'}
					<div class="tab-redirect">
						<p>Key controls are available in the GFX panel.</p>
						<button class="btn-open-gfx" onclick={openGfxPanel}>Open GFX Panel</button>
					</div>
				{:else if activeTab === 'video'}
					<div class="tab-panel-wrap">
						<VideoProcessingPanel state={crState} />
					</div>
				{:else if activeTab === 'encoder'}
					<div class="tab-panel-wrap encoder-settings">
						<div class="settings-row">
							<span class="settings-label">Current Encoder</span>
							<span class="settings-value">{crState.encoder?.current ?? 'Default'}</span>
						</div>
						{#if crState.encoder?.available && crState.encoder.available.length > 0}
							<div class="settings-row">
								<span class="settings-label">Available Encoders</span>
								<div class="encoder-list">
									{#each crState.encoder.available as enc}
										<button
											class="encoder-option"
											class:encoder-active={enc.name === crState.encoder?.current}
											disabled={encoderChanging || enc.name === crState.encoder?.current}
											onclick={() => handleEncoderChange(enc.name)}
										>
											{enc.displayName}
											{#if enc.isDefault}
												<span class="encoder-default-badge">default</span>
											{/if}
										</button>
									{/each}
								</div>
							</div>
						{/if}
					</div>
				{:else if activeTab === 'format'}
					<div class="tab-panel-wrap format-settings">
						<div class="settings-row">
							<span class="settings-label">Pipeline Format</span>
							<span class="settings-value">
								{crState.pipelineFormat?.width ?? '?'}&times;{crState.pipelineFormat?.height ?? '?'}
								@ {crState.pipelineFormat?.fpsNum ?? '?'}/{crState.pipelineFormat?.fpsDen ?? 1} fps
							</span>
						</div>
						{#if crState.pipelineFormat?.name}
							<div class="settings-row">
								<span class="settings-label">Preset</span>
								<span class="settings-value">{crState.pipelineFormat.name}</span>
							</div>
						{/if}
						{#if crState.pipelineFormat?.colorFormat}
							<div class="settings-row">
								<span class="settings-label">Color Format</span>
								<span class="settings-value">{crState.pipelineFormat.colorFormat}</span>
							</div>
						{/if}
						{#if formatPresets.length > 0}
							<div class="settings-row">
								<span class="settings-label">Change Format</span>
								<div class="format-list">
									{#each formatPresets as preset}
										<button
											class="format-option"
											class:format-active={preset === crState.pipelineFormat?.name}
											disabled={preset === crState.pipelineFormat?.name}
											onclick={() => handleFormatChange(preset)}
										>
											{preset}
										</button>
									{/each}
								</div>
							</div>
						{/if}
					</div>
				{:else if activeTab === 'team'}
					<div class="tab-panel-wrap">
						<TeamPanel state={crState} />
					</div>
				{/if}
			</div>
		</div>
	</div>
{/if}

<style>
	.settings-backdrop {
		position: fixed;
		inset: 0;
		background: var(--overlay-heavy);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: var(--z-modal, 200);
	}

	.settings-modal {
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
		width: 80%;
		height: 70%;
		max-width: 900px;
		max-height: 600px;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.settings-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border-default);
		flex-shrink: 0;
	}

	.settings-title {
		margin: 0;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-primary);
	}

	.close-btn {
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: 18px;
		line-height: 1;
		cursor: pointer;
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast);
	}

	.close-btn:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	/* ── Tab bar ── */
	.tab-bar {
		display: flex;
		gap: 0;
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-default);
		flex-shrink: 0;
		height: 30px;
	}

	.tab {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-secondary);
		background: transparent;
		border: none;
		border-bottom: 2px solid transparent;
		padding: 0 14px;
		cursor: pointer;
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast),
			background var(--transition-fast);
		display: flex;
		align-items: center;
		letter-spacing: 0.01em;
	}

	.tab:hover {
		color: var(--text-primary);
		background: rgba(255, 255, 255, 0.04);
	}

	.tab.active {
		color: var(--text-primary);
		font-weight: 600;
		border-bottom-color: var(--accent-yellow);
		background: rgba(255, 255, 255, 0.03);
	}

	/* ── Tab content ── */
	.tab-content {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.tab-panel-wrap {
		padding: 12px 16px;
	}

	/* ── Redirect tab ── */
	.tab-redirect {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 12px;
		padding: 48px 24px;
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
	}

	.tab-redirect p {
		margin: 0;
	}

	.btn-open-gfx {
		padding: 6px 16px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-control);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.btn-open-gfx:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	/* ── Settings rows ── */
	.settings-row {
		display: flex;
		align-items: flex-start;
		gap: 12px;
		padding: 10px 0;
		border-bottom: 1px solid var(--border-subtle);
	}

	.settings-row:last-child {
		border-bottom: none;
	}

	.settings-label {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-secondary);
		min-width: 120px;
		flex-shrink: 0;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.settings-value {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--text-primary);
	}

	/* ── Encoder list ── */
	.encoder-list,
	.format-list {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
	}

	.encoder-option,
	.format-option {
		padding: 4px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-control);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 500;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.encoder-option:hover:not(:disabled),
	.format-option:hover:not(:disabled) {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	.encoder-option:disabled,
	.format-option:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.encoder-active,
	.format-active {
		background: var(--color-success-dim);
		border-color: rgba(34, 197, 94, 0.4);
		color: var(--color-success);
	}

	.encoder-default-badge {
		font-size: 10px;
		font-weight: 400;
		color: var(--text-tertiary);
		margin-left: 4px;
	}
</style>
