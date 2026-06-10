<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';

	type Status = { version: string; commit: string; uptime: string; device_count: number };
	type Device = {
		id: string; hostname: string; os: string; mesh_ip: string;
		last_seen: string | null; agent_version: string; online: boolean;
	};

	let status = $state<Status | null>(null);
	let devices = $state<Device[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	async function load() {
		try {
			const [sr, dr] = await Promise.all([
				fetch('/api/v1/status'),
				fetch('/api/v1/devices'),
			]);
			if (sr.status === 401 || dr.status === 401) { goto('/login'); return; }
			if (!sr.ok) throw new Error((await sr.json()).error);
			if (!dr.ok) throw new Error((await dr.json()).error);
			const [s, d] = await Promise.all([sr.json(), dr.json()]);
			status = s;
			devices = d;
			error = null;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		load();
		const t = setInterval(load, 30_000);
		return () => clearInterval(t);
	});

	function timeAgo(dateStr: string | null): string {
		if (!dateStr) return 'never';
		const d = new Date(dateStr);
		const diff = Math.floor((Date.now() - d.getTime()) / 1000);
		if (diff < 60) return 'just now';
		if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
		if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
		return `${Math.floor(diff / 86400)}d ago`;
	}

	const onlineCount = $derived(devices.filter(d => d.online).length);
</script>

<svelte:head><title>Dashboard · Zeta</title></svelte:head>

<div class="page-header">
	<div class="page-title">Dashboard</div>
	<div class="page-sub">Network overview</div>
</div>

{#if loading}
	<div class="loading">Loading…</div>
{:else if error}
	<div class="table-wrap"><div class="empty"><div class="empty-icon">⚠️</div><p>API error: {error}</p></div></div>
{:else}
	<div class="stats-grid">
		<div class="stat-card">
			<div class="stat-label">Total Devices</div>
			<div class="stat-value">{status?.device_count ?? 0}</div>
		</div>
		<div class="stat-card">
			<div class="stat-label">Online Now</div>
			<div class="stat-value" style="color: var(--online)">{onlineCount}</div>
		</div>
		<div class="stat-card">
			<div class="stat-label">Uptime</div>
			<div class="stat-value mono">{status?.uptime ?? '—'}</div>
		</div>
		<div class="stat-card">
			<div class="stat-label">Version</div>
			<div class="stat-value mono">{status?.version ?? '—'}</div>
		</div>
	</div>

	<div class="section-header">
		<div class="section-title">Recent Devices</div>
		<a href="/devices" class="btn btn-ghost btn-sm">View all</a>
	</div>

	<div class="table-wrap">
		{#if devices.length === 0}
			<div class="empty">
				<div class="empty-icon">📡</div>
				<p>No devices enrolled yet.</p>
			</div>
		{:else}
			<table>
				<thead>
					<tr>
						<th>Status</th>
						<th>Hostname</th>
						<th>Mesh IP</th>
						<th>OS</th>
						<th>Last Seen</th>
					</tr>
				</thead>
				<tbody>
					{#each devices.slice(0, 8) as dev}
						<tr>
							<td>
								<span class="badge {dev.online ? 'badge-online' : 'badge-offline'}">
									<span class="badge-dot"></span>
									{dev.online ? 'online' : 'offline'}
								</span>
							</td>
							<td style="font-weight:600">{dev.hostname}</td>
							<td class="mono">{dev.mesh_ip}</td>
							<td class="sub">{dev.os}</td>
							<td class="sub">{timeAgo(dev.last_seen)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{/if}
	</div>
{/if}
