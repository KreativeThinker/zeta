<script lang="ts">
	import { onMount } from 'svelte';

	type AuditEntry = {
		id: number; event: string; device_id: string | null;
		metadata: string | null; created_at: string;
	};

	let entries = $state<AuditEntry[]>([]);
	let loading = $state(true);
	let loadingMore = $state(false);
	let offset = $state(0);
	let hasMore = $state(true);
	const LIMIT = 50;

	async function load(reset = false) {
		if (reset) { offset = 0; entries = []; hasMore = true; }
		const res = await fetch(`/api/v1/audit-log?limit=${LIMIT}&offset=${offset}`).then(r => r.json());
		const batch: AuditEntry[] = res ?? [];
		entries = reset ? batch : [...entries, ...batch];
		offset += batch.length;
		hasMore = batch.length === LIMIT;
		loading = false;
		loadingMore = false;
	}

	async function loadMore() {
		loadingMore = true;
		await load();
	}

	onMount(() => load(true));

	function fmt(dateStr: string): string {
		const d = new Date(dateStr);
		return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}

	function parseMeta(raw: string | null): string {
		if (!raw) return '';
		try {
			const obj = JSON.parse(raw);
			return Object.entries(obj).map(([k, v]) => `${k}=${v}`).join(' · ');
		} catch {
			return raw;
		}
	}

	function eventColor(event: string): string {
		if (event.includes('delete') || event.includes('revoke')) return 'var(--danger)';
		if (event.includes('enroll') || event.includes('create')) return 'var(--online)';
		return 'var(--accent)';
	}
</script>

<svelte:head><title>Audit Log · Zeta</title></svelte:head>

<div class="top-bar">
	<div>
		<div class="page-title">Audit Log</div>
		<div class="page-sub">All system events</div>
	</div>
	<button class="btn btn-ghost btn-sm" onclick={() => load(true)}>Refresh</button>
</div>

{#if loading}
	<div class="loading">Loading…</div>
{:else if entries.length === 0}
	<div class="table-wrap">
		<div class="empty">
			<div class="empty-icon">📋</div>
			<p>No events recorded yet.</p>
		</div>
	</div>
{:else}
	<div class="table-wrap">
		{#each entries as entry}
			<div class="audit-entry">
				<div style="flex:1;min-width:0">
					<div class="audit-event" style="color:{eventColor(entry.event)}">{entry.event}</div>
					{#if entry.device_id}
						<div class="audit-meta">device: {entry.device_id}</div>
					{/if}
					{#if parseMeta(entry.metadata)}
						<div class="audit-meta">{parseMeta(entry.metadata)}</div>
					{/if}
				</div>
				<div class="audit-time">{fmt(entry.created_at)}</div>
			</div>
		{/each}

		{#if hasMore}
			<div style="padding:1rem;text-align:center;border-top:1px solid var(--border)">
				<button class="btn btn-ghost btn-sm" onclick={loadMore} disabled={loadingMore}>
					{loadingMore ? 'Loading…' : 'Load more'}
				</button>
			</div>
		{/if}
	</div>
{/if}
