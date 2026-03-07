<script lang="ts">
	import { tick } from 'svelte';
	import type { ControlRoomState, SRTOutputConfig, DestinationConfig, DestinationInfo } from '$lib/api/types';
	import { startSRTOutput, stopSRTOutput, addDestination, removeDestination, startDestination, stopDestination, apiCall } from '$lib/api/switch-api';
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

	// New destination form
	let destForm = $state({
		type: 'srt-caller' as 'srt-caller' | 'srt-listener',
		address: '',
		port: 9000,
		streamID: '',
		latency: 200,
		name: '',
	});
	let showAddDest = $state(false);

	const isActive = $derived(crState.srtOutput?.active ?? false);
	const destinations = $derived(crState.destinations ?? []);
	let confirmingStop = $state(false);
	let confirmingRemoveId = $state<string | null>(null);

	const isCallerAddressEmpty = $derived(form.mode === 'caller' && !form.address.trim());
	const isDestCallerAddressEmpty = $derived(destForm.type === 'srt-caller' && !destForm.address.trim());

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

	// --- Destination handlers ---

	function handleAddDestination() {
		if (isDestCallerAddressEmpty) return;
		const config: DestinationConfig = {
			type: destForm.type,
			port: destForm.port,
			name: destForm.name || undefined,
		};
		if (destForm.type === 'srt-caller') {
			config.address = destForm.address;
			if (destForm.streamID) config.streamID = destForm.streamID;
		}
		if (destForm.latency > 0) config.latency = destForm.latency;
		apiCall(addDestination(config), 'Add destination failed');
		showAddDest = false;
		// Reset form
		destForm.address = '';
		destForm.name = '';
		destForm.streamID = '';
	}

	function handleStartDestination(id: string) {
		apiCall(startDestination(id), 'Start destination failed');
	}

	function handleStopDestination(id: string) {
		apiCall(stopDestination(id), 'Stop destination failed');
	}

	function handleRemoveDestination(id: string) {
		confirmingRemoveId = id;
	}

	function confirmRemove() {
		if (confirmingRemoveId) {
			apiCall(removeDestination(confirmingRemoveId), 'Remove destination failed');
			confirmingRemoveId = null;
		}
	}

	function cancelRemove() {
		confirmingRemoveId = null;
	}

	function destStateColor(state: string): string {
		switch (state) {
			case 'active': return 'var(--tally-preview, #22c55e)';
			case 'reconnecting': return 'var(--accent-amber, #f59e0b)';
			case 'error': return 'var(--tally-program, #ef4444)';
			default: return 'var(--text-tertiary)';
		}
	}

	function destLabel(d: DestinationInfo): string {
		if (d.name) return d.name;
		if (d.address) return `${d.address}:${d.port}`;
		return `:${d.port}`;
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

			<!-- Legacy single SRT output -->
			{#if isActive}
				<div class="srt-status">
					<div class="section-label">Legacy Output</div>
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
			{:else if destinations.length === 0}
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

			<!-- Multi-destination list -->
			{#if destinations.length > 0 || !isActive}
				<div class="dest-section">
					<div class="dest-header">
						<span class="section-label">Destinations</span>
						<button class="add-dest-btn" onclick={() => showAddDest = !showAddDest}>
							{showAddDest ? 'Cancel' : '+ Add'}
						</button>
					</div>

					{#if showAddDest}
						<div class="dest-add-form">
							<div class="form-field">
								<label for="dest-name">Name</label>
								<input id="dest-name" type="text" bind:value={destForm.name} placeholder="YouTube, Twitch, etc." />
							</div>
							<div class="mode-selector">
								<label class="mode-option" class:selected={destForm.type === 'srt-caller'}>
									<input type="radio" value="srt-caller" bind:group={destForm.type} />
									Caller
								</label>
								<label class="mode-option" class:selected={destForm.type === 'srt-listener'}>
									<input type="radio" value="srt-listener" bind:group={destForm.type} />
									Listener
								</label>
							</div>
							{#if destForm.type === 'srt-caller'}
								<div class="form-field">
									<label for="dest-address">Address</label>
									<input id="dest-address" type="text" bind:value={destForm.address} placeholder="192.168.1.100" />
								</div>
							{/if}
							<div class="form-field">
								<label for="dest-port">Port</label>
								<input id="dest-port" type="number" bind:value={destForm.port} min="1" max="65535" />
							</div>
							{#if destForm.type === 'srt-caller'}
								<div class="form-field">
									<label for="dest-stream-id">Stream ID</label>
									<input id="dest-stream-id" type="text" bind:value={destForm.streamID} placeholder="(optional)" />
								</div>
							{/if}
							<div class="form-field">
								<label for="dest-latency">Latency (ms)</label>
								<input id="dest-latency" type="number" bind:value={destForm.latency} min="0" step="50" />
							</div>
							<button class="modal-btn start-btn" onclick={handleAddDestination} disabled={isDestCallerAddressEmpty}>
								Add Destination
							</button>
						</div>
					{/if}

					{#each destinations as dest (dest.id)}
						<div class="dest-item">
							<div class="dest-item-header">
								<span class="dest-name">{destLabel(dest)}</span>
								<span class="dest-state" style="color: {destStateColor(dest.state)}">{dest.state}</span>
							</div>
							<div class="dest-item-meta">
								<span class="dest-type">{dest.type}</span>
								{#if dest.connections && dest.connections > 0}
									<span class="dest-conns">{dest.connections} conn</span>
								{/if}
								{#if (dest.droppedPackets ?? 0) > 0}
									<span class="dest-drops">{dest.droppedPackets} dropped</span>
								{/if}
							</div>
							{#if dest.error}
								<div class="dest-error">{dest.error}</div>
							{/if}
							<div class="dest-actions">
								{#if dest.state === 'stopped'}
									<button class="dest-action-btn start-action" onclick={() => handleStartDestination(dest.id)}>Start</button>
								{:else}
									<button class="dest-action-btn stop-action" onclick={() => handleStopDestination(dest.id)}>Stop</button>
								{/if}
								<button class="dest-action-btn delete-action" onclick={() => handleRemoveDestination(dest.id)}>Delete</button>
							</div>
						</div>
					{/each}
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

<ConfirmDialog
	open={confirmingRemoveId !== null}
	title="Remove Destination"
	message="Remove this output destination? If active, it will be disconnected."
	confirmLabel="Remove"
	onconfirm={confirmRemove}
	oncancel={cancelRemove}
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
		max-width: 440px;
		max-height: 80vh;
		overflow-y: auto;
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

	.section-label {
		font-size: 0.65rem;
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		margin-bottom: 8px;
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

	/* Destination section */
	.dest-section {
		margin-top: 16px;
		border-top: 1px solid var(--border-subtle);
		padding-top: 12px;
	}

	.dest-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 8px;
	}

	.dest-header .section-label {
		margin-bottom: 0;
	}

	.add-dest-btn {
		background: none;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--accent-blue);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		padding: 3px 10px;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast);
	}

	.add-dest-btn:hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.dest-add-form {
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: 12px;
		margin-bottom: 12px;
	}

	.dest-item {
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: 10px 12px;
		margin-bottom: 6px;
	}

	.dest-item-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 4px;
	}

	.dest-name {
		font-size: 0.8rem;
		font-weight: 600;
		color: var(--text-primary);
	}

	.dest-state {
		font-size: 0.7rem;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.dest-item-meta {
		display: flex;
		gap: 10px;
		font-size: 0.7rem;
		color: var(--text-tertiary);
		margin-bottom: 6px;
	}

	.dest-type {
		font-family: var(--font-mono);
		font-size: 0.65rem;
	}

	.dest-drops {
		color: var(--accent-amber, #f59e0b);
	}

	.dest-error {
		font-size: 0.7rem;
		color: var(--tally-program);
		margin-bottom: 6px;
	}

	.dest-actions {
		display: flex;
		gap: 6px;
	}

	.dest-action-btn {
		flex: 1;
		padding: 4px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast);
	}

	.start-action:hover {
		border-color: var(--tally-preview, #22c55e);
		background: rgba(34, 197, 94, 0.1);
	}

	.stop-action {
		border-color: rgba(220, 38, 38, 0.3);
		color: var(--tally-program);
	}

	.stop-action:hover {
		background: var(--tally-program-dim);
	}

	.delete-action {
		flex: 0;
		padding: 4px 8px;
		color: var(--text-tertiary);
	}

	.delete-action:hover {
		color: var(--tally-program);
		border-color: rgba(220, 38, 38, 0.3);
	}
</style>
