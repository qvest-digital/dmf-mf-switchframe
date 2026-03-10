<script lang="ts">
	function formatTime(date: Date): string {
		const h = String(date.getHours()).padStart(2, '0');
		const m = String(date.getMinutes()).padStart(2, '0');
		const s = String(date.getSeconds()).padStart(2, '0');
		return `${h}:${m}:${s}`;
	}

	let now = $state(formatTime(new Date()));

	$effect(() => {
		const id = setInterval(() => {
			now = formatTime(new Date());
		}, 1000);
		return () => clearInterval(id);
	});
</script>

<span class="clock">{now}</span>

<style>
	.clock {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-secondary);
		letter-spacing: 0.04em;
		user-select: none;
	}
</style>
