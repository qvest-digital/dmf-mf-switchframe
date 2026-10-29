<script lang="ts">
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { version } from '$app/environment';
	import { notify } from '$lib/state/notifications.svelte';
	import { watchStaleBundle } from '$lib/util/stale-bundle';

	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();

	onMount(() =>
		watchStaleBundle({
			current: version,
			fetchVersion: async () => {
				const res = await fetch('/_app/version.json', { cache: 'no-store' });
				if (!res.ok) throw new Error(`HTTP ${res.status}`);
				return (await res.json()).version;
			},
			reload: () => location.reload(),
			prompt: () => notify('warning', 'A newer version of this page is available: reload to use it.'),
			isHidden: () => document.hidden,
			storage: sessionStorage,
		}),
	);
</script>

{@render children()}
