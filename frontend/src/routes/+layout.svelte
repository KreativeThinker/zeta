<script lang="ts">
	import '../app.css';
	import { page } from '$app/stores';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	const links = [
		{ href: '/', label: 'Dashboard', icon: 'grid' },
		{ href: '/devices', label: 'Devices', icon: 'monitor' },
		{ href: '/keys', label: 'Preauth Keys', icon: 'key' },
		{ href: '/audit', label: 'Audit Log', icon: 'list' },
	];

	function isActive(href: string, currentPath: string) {
		if (href === '/') return currentPath === '/';
		return currentPath.startsWith(href);
	}
</script>

<div class="shell">
	<aside class="sidebar">
		<div class="sidebar-brand">
			Zeta
			<span>network admin</span>
		</div>

		{#each links as link}
			<a
				href={link.href}
				class="nav-link"
				class:active={isActive(link.href, $page.url.pathname)}
			>
				{#if link.icon === 'grid'}
					<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg>
				{:else if link.icon === 'monitor'}
					<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
				{:else if link.icon === 'key'}
					<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="7.5" cy="15.5" r="5.5"/><path d="M21 2l-9.6 9.6M15.5 7.5l3 3"/></svg>
				{:else if link.icon === 'list'}
					<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01"/></svg>
				{/if}
				{link.label}
			</a>
		{/each}
	</aside>

	<main class="main">
		{@render children?.()}
	</main>
</div>
