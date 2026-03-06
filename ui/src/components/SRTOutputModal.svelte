<script lang="ts">
	import { tick } from 'svelte';
	import type { ControlRoomState, SRTOutputConfig } from '$lib/api/types';
	import { startSRTOutput, stopSRTOutput, apiCall } from '$lib/api/switch-api';
	import ConfirmDialog from './ConfirmDialog.svelte';

	interface Props {
		state: ControlRoomState;
		visible: boolean;
		onclose?: () => void;
	}
	let { state: crState, visible, onclose }: Props = $props();

	let form = $state({
		mode: 'caller' as 'caller' | 'listener',
		address: '',
		port: 9000,
		streamID: '',
		latency: 200,
	});

	const isActive = $derived(crState.srtOutput?.active ?? false);
	let confirmingStop = $state(false);

	const isCallerAddressEmpty = $derived(form.mode === 'caller' && !form.address.trim());

	let modalElement: HTMLDivElement | undefined = $state();

	// Focus trap: when modal becomes visible, focus the first focusable element
	$effect(() => {
		if (visible) {
			tick().then(() => {
				if (modalElement) {
					const first = getFocusableElements()?.[0];
					if (first) first.focus();
				}
			});
		}
	});

	function getFocusableElements(): HTMLElement[] {
		if (!modalElement) return [];
		return Array.from(
			modalElement.querySelectorAll<HTMLElement>(
				'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
			),
		);
	}

	function handleModalKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			handleClose();
			return;
		}

		// Focus trap: wrap Tab navigation within the modal
		if (e.key === 'Tab') {
			const focusable = getFocusableElements();
			if (focusable.length === 0) return;

			const first = focusable[0];
			const last = focusable[focusable.length - 1];

			if (e.shiftKey) {
				if (document.activeElement === first) {
					e.preventDefault();
					last.focus();
				}
			} else {
				if (document.activeElement === last) {
					e.preventDefault();
					first.focus();
				}
			}
		}
	}

	function handleStart() {
		if (isCallerAddressEmpty) return;
		const config: SRTOutputConfig = { mode: form.mode, port: form.port };
		if (form.mode === 'caller') {
			config.address = form.address;
			if (form.streamID) config.streamID = form.streamID;
		}
		if (form.latency > 0) config.latency = form.latency;
		apiCall(startSRTOutput(config), 'SRT start failed');
	}

	function handleStop() {
		confirmingStop = true;
	}

	function confirmStop() {
		apiCall(stopSRTOutput(), 'SRT stop failed');
		confirmingStop = false;
	}

	function cancelStop() {
		confirmingStop = false;
	}

	function handleClose() {
		onclose?.();
	}
</script>

{#if visible}
	<div class="srt-modal-backdrop" role="presentation" onclick={handleClose}>
		<div
			class="srt-modal"
			role="dialog"
			aria-modal="true"
			aria-labelledby="srt-modal-title"
			tabindex="-1"
			bind:this={modalElement}
			onclick={(e) => e.stopPropagation()}
			onkeydown={handleModalKeydown}
		>
			<div class="modal-header">
				<h3 id="srt-modal-title">SRT Output</h3>
				<button class="close-btn" onclick={handleClose}>&#x2715;</button>
			</div>

			{#if isActive}
				<div class="srt-status">
					<div class="status-row">
						<span class="status-label">Mode</span>
						<span class="status-value">{crState.srtOutput?.mode ?? ''}</span>
					</div>
					{#if crState.srtOutput?.address}
						<div class="status-row">
							<span class="status-label">Address</span>
							<span class="status-value">{crState.srtOutput.address}:{crState.srtOutput.port}</span>
						</div>
					{/if}
					{#if crState.srtOutput?.state}
						<div class="status-row">
							<span class="status-label">State</span>
							<span class="status-value">{crState.srtOutput.state}</span>
						</div>
					{/if}
					{#if crState.srtOutput?.mode === 'listener'}
						<div class="status-row">
							<span class="status-label">Connections</span>
							<span class="status-value">{crState.srtOutput?.connections ?? 0}</span>
						</div>
					{/if}
					{#if (crState.srtOutput?.droppedPackets ?? 0) > 0}
						<div class="status-row drop-warn-row">
							<span class="status-label">Dropped</span>
							<span class="status-value drop-warn-value">{crState.srtOutput?.droppedPackets}</span>
						</div>
					{/if}
					{#if (crState.srtOutput?.overflowCount ?? 0) > 0}
						<div class="status-row drop-warn-row">
							<span class="status-label">Overflows</span>
							<span class="status-value drop-warn-value">{crState.srtOutput?.overflowCount}</span>
						</div>
					{/if}
					<button class="modal-btn stop-btn" onclick={handleStop}>Stop</button>
				</div>
			{:else}
				<div class="srt-form">
					<div class="mode-selector">
						<label class="mode-option" class:selected={form.mode === 'caller'}>
							<input type="radio" value="caller" bind:group={form.mode} />
							Caller
						</label>
						<label class="mode-option" class:selected={form.mode === 'listener'}>
							<input type="radio" value="listener" bind:group={form.mode} />
							Listener
						</label>
					</div>

					{#if form.mode === 'caller'}
						<div class="form-field">
							<label for="srt-address">Address</label>
							<input id="srt-address" type="text" name="address" bind:value={form.address} placeholder="192.168.1.100" />
						</div>
					{/if}

					<div class="form-field">
						<label for="srt-port">Port</label>
						<input id="srt-port" type="number" name="port" bind:value={form.port} min="1" max="65535" />
					</div>

					{#if form.mode === 'caller'}
						<div class="form-field">
							<label for="srt-stream-id">Stream ID</label>
							<input id="srt-stream-id" type="text" name="streamID" bind:value={form.streamID} placeholder="(optional)" />
						</div>
					{/if}

					<div class="form-field">
						<label for="srt-latency">Latency (ms)</label>
						<input id="srt-latency" type="number" name="latency" bind:value={form.latency} min="0" step="50" />
					</div>

					<button class="modal-btn start-btn" onclick={handleStart} disabled={isCallerAddressEmpty}>Start</button>
				</div>
			{/if}
		</div>
	</div>
{/if}

<ConfirmDialog
	open={confirmingStop}
	title="Disconnect SRT"
	message="Disconnect SRT output? The stream will be interrupted."
	confirmLabel="Disconnect"
	onconfirm={confirmStop}
	oncancel={cancelStop}
/>

<style>
	.srt-modal-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
	}

	.srt-modal {
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		padding: 20px;
		min-width: 320px;
		max-width: 400px;
		font-family: var(--font-ui);
		color: var(--text-primary);
		box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 16px;
	}

	.modal-header h3 {
		margin: 0;
		font-size: 0.9rem;
		font-weight: 600;
		color: var(--text-primary);
	}

	.close-btn {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 0.9rem;
		padding: 4px;
		border-radius: var(--radius-sm);
		transition: color var(--transition-fast);
	}

	.close-btn:hover {
		color: var(--text-primary);
	}

	.mode-selector {
		display: flex;
		gap: 2px;
		margin-bottom: 16px;
		background: var(--bg-base);
		border-radius: var(--radius-md);
		padding: 2px;
		border: 1px solid var(--border-subtle);
	}

	.mode-option {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 0;
		font-size: 0.8rem;
		font-weight: 500;
		cursor: pointer;
		color: var(--text-secondary);
		padding: 5px 12px;
		border-radius: var(--radius-sm);
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.mode-option:hover {
		color: var(--text-primary);
	}

	.mode-option.selected {
		background: var(--bg-elevated);
		color: var(--accent-blue);
	}

	.mode-option input {
		display: none;
	}

	.form-field {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-bottom: 12px;
	}

	.form-field label {
		font-size: 0.65rem;
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.form-field input {
		padding: 7px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		background: var(--bg-base);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: 0.8rem;
		transition: border-color var(--transition-fast);
	}

	.form-field input:focus {
		outline: none;
		border-color: var(--accent-blue);
	}

	.form-field input::placeholder {
		color: var(--text-tertiary);
	}

	.modal-btn {
		padding: 8px 16px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 0.8rem;
		letter-spacing: 0.02em;
		width: 100%;
		margin-top: 8px;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast);
	}

	.modal-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.start-btn:hover:not(:disabled) {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.stop-btn {
		border-color: rgba(220, 38, 38, 0.4);
		color: var(--tally-program);
	}

	.stop-btn:hover {
		background: var(--tally-program-dim);
	}

	.srt-status {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.status-row {
		display: flex;
		justify-content: space-between;
		font-size: 0.8rem;
	}

	.status-label {
		color: var(--text-tertiary);
		font-size: 0.7rem;
		font-weight: 500;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.status-value {
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: 0.8rem;
	}

	.drop-warn-row {
		border-top: 1px solid rgba(245, 158, 11, 0.2);
		padding-top: 6px;
	}

	.drop-warn-value {
		color: var(--accent-amber, #f59e0b);
		font-weight: 600;
	}
</style>
