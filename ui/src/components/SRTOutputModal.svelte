<script lang="ts">
	import type { ControlRoomState, SRTOutputConfig } from '$lib/api/types';
	import { startSRTOutput, stopSRTOutput, fireAndForget } from '$lib/api/switch-api';

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

	function handleStart() {
		const config: SRTOutputConfig = { mode: form.mode, port: form.port };
		if (form.mode === 'caller') {
			config.address = form.address;
			if (form.streamID) config.streamID = form.streamID;
		}
		if (form.latency > 0) config.latency = form.latency;
		fireAndForget(startSRTOutput(config));
	}

	function handleStop() {
		fireAndForget(stopSRTOutput());
	}

	function handleClose() {
		onclose?.();
	}
</script>

{#if visible}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="srt-modal-backdrop" onclick={handleClose} onkeydown={() => {}}>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="srt-modal" onclick={(e) => e.stopPropagation()} onkeydown={() => {}}>
			<div class="modal-header">
				<h3>SRT Output</h3>
				<button class="close-btn" onclick={handleClose}>X</button>
			</div>

			{#if isActive}
				<div class="srt-status">
					<div class="status-row">
						<span class="status-label">Mode:</span>
						<span class="status-value">{crState.srtOutput?.mode ?? ''}</span>
					</div>
					{#if crState.srtOutput?.address}
						<div class="status-row">
							<span class="status-label">Address:</span>
							<span class="status-value">{crState.srtOutput.address}:{crState.srtOutput.port}</span>
						</div>
					{/if}
					{#if crState.srtOutput?.state}
						<div class="status-row">
							<span class="status-label">State:</span>
							<span class="status-value">{crState.srtOutput.state}</span>
						</div>
					{/if}
					{#if crState.srtOutput?.mode === 'listener'}
						<div class="status-row">
							<span class="status-label">Connections:</span>
							<span class="status-value">{crState.srtOutput?.connections ?? 0}</span>
						</div>
					{/if}
					<button class="btn stop-btn" onclick={handleStop}>Stop</button>
				</div>
			{:else}
				<div class="srt-form">
					<div class="mode-selector">
						<label class="mode-option">
							<input type="radio" value="caller" bind:group={form.mode} />
							Caller
						</label>
						<label class="mode-option">
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

					<button class="btn start-btn" onclick={handleStart}>Start</button>
				</div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.srt-modal-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.7);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
	}

	.srt-modal {
		background: #1a1a1a;
		border: 1px solid #444;
		border-radius: 8px;
		padding: 1.25rem;
		min-width: 320px;
		max-width: 420px;
		font-family: monospace;
		color: #ccc;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 1rem;
	}

	.modal-header h3 {
		margin: 0;
		font-size: 1rem;
		color: #eee;
	}

	.close-btn {
		background: none;
		border: none;
		color: #888;
		cursor: pointer;
		font-family: monospace;
		font-size: 1rem;
		padding: 0.25rem;
	}

	.close-btn:hover { color: #ccc; }

	.mode-selector {
		display: flex;
		gap: 1rem;
		margin-bottom: 1rem;
	}

	.mode-option {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.85rem;
		cursor: pointer;
		color: #ccc;
	}

	.mode-option input { accent-color: #4488ff; }

	.form-field {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		margin-bottom: 0.75rem;
	}

	.form-field label {
		font-size: 0.75rem;
		color: #888;
	}

	.form-field input {
		padding: 0.4rem 0.5rem;
		border: 1px solid #444;
		border-radius: 4px;
		background: #222;
		color: #ccc;
		font-family: monospace;
		font-size: 0.85rem;
	}

	.form-field input:focus {
		outline: none;
		border-color: #4488ff;
	}

	.btn {
		padding: 0.5rem 1rem;
		border: 2px solid #444;
		border-radius: 4px;
		background: #222;
		color: #ccc;
		cursor: pointer;
		font-family: monospace;
		font-weight: bold;
		font-size: 0.85rem;
		width: 100%;
		margin-top: 0.5rem;
	}

	.start-btn:hover { border-color: #4488ff; background: #1a2a44; }
	.stop-btn { border-color: #cc0000; }
	.stop-btn:hover { background: #2a0000; }

	.srt-status { display: flex; flex-direction: column; gap: 0.5rem; }

	.status-row {
		display: flex;
		justify-content: space-between;
		font-size: 0.85rem;
	}

	.status-label { color: #888; }
	.status-value { color: #ccc; }
</style>
