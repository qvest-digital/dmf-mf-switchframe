<script lang="ts">
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { updated } from '$app/state';
	import { notify } from '$lib/state/notifications.svelte';
	import { watchStaleBundle } from '$lib/util/stale-bundle';

	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();

	onMount(() =>
		watchStaleBundle({
			check: () => updated.check(),
			reload: () => location.reload(),
			isHidden: () => document.hidden,
			prompt: () => notify('warning', 'A newer version of this page is available: reload to use it.'),
		}),
	);
</script>

{@render children()}
