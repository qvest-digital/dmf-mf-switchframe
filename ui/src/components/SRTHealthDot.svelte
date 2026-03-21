<script lang="ts">
	type SRTHealthLevel = 'green' | 'yellow' | 'red' | 'gray';

	interface Props {
		level: SRTHealthLevel;
		onclick?: (e: MouseEvent) => void;
	}

	let { level, onclick }: Props = $props();

	const titles: Record<SRTHealthLevel, string> = {
		green: 'SRT healthy',
		yellow: 'SRT degraded',
		red: 'SRT critical',
		gray: 'SRT disconnected',
	};
</script>

{#if onclick}
	<button
		class="srt-dot {level}"
		{onclick}
		title={titles[level]}
	></button>
{:else}
	<span
		class="srt-dot {level}"
		title={titles[level]}
	></span>
{/if}

<style>
	.srt-dot {
		display: inline-block;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		border: none;
		padding: 0;
		flex-shrink: 0;
		transition: background-color 0.3s ease;
	}

	button.srt-dot {
		cursor: pointer;
		background: none;
	}

	button.srt-dot:hover {
		transform: scale(1.3);
	}

	.srt-dot.green {
		background-color: #22c55e;
		box-shadow: 0 0 4px rgba(34, 197, 94, 0.5);
	}

	.srt-dot.yellow {
		background-color: #eab308;
		box-shadow: 0 0 4px rgba(234, 179, 8, 0.5);
		animation: pulse-yellow 2s ease-in-out 1;
	}

	.srt-dot.red {
		background-color: #ef4444;
		box-shadow: 0 0 4px rgba(239, 68, 68, 0.5);
		animation: pulse-red 1s ease-in-out 3;
	}

	.srt-dot.gray {
		background-color: #6b7280;
		box-shadow: none;
	}

	@keyframes pulse-yellow {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	@keyframes pulse-red {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}
</style>
