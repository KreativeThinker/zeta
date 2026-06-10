<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';

	type Service = {
		id: string;
		device_id: string;
		device_hostname: string;
		device_mesh_ip: string;
		name: string;
		port: number;
		target_addr: string;
		allowed_pubkeys: string[] | null;
	};

	type Device = {
		id: string;
		hostname: string;
		wg_public_key: string;
		mesh_ip: string;
		online: boolean;
	};

	let services = $state<Service[]>([]);
	let devices = $state<Device[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	async function load() {
		try {
			const [sr, dr] = await Promise.all([
				fetch('/api/v1/services'),
				fetch('/api/v1/devices'),
			]);
			if (sr.status === 401 || dr.status === 401) { goto('/login'); return; }
			if (!sr.ok) throw new Error((await sr.json()).error);
			if (!dr.ok) throw new Error((await dr.json()).error);
			services = await sr.json();
			devices = await dr.json();
			error = null;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		load();
		const t = setInterval(load, 15_000);
		return () => clearInterval(t);
	});

	// Map pubkey → hostname for display
	const pkToHostname = $derived(
		Object.fromEntries(devices.map(d => [d.wg_public_key, d.hostname]))
	);

	const hostOnline = $derived(
		Object.fromEntries(devices.map(d => [d.hostname, d.online]))
	);

	function domain(svc: Service): string {
		return `${svc.name}.${svc.device_hostname}.mesh`;
	}
</script>

<svelte:head><title>Services · Zeta</title></svelte:head>

<div class="top-bar">
	<div>
		<div class="page-title">Services</div>
		<div class="page-sub">{services.length} service{services.length === 1 ? '' : 's'} exposed across the mesh</div>
	</div>
</div>

{#if loading}
	<div class="loading">Loading…</div>
{:else if error}
	<div class="table-wrap">
		<div class="empty"><div class="empty-icon">⚠</div><p>API error: {error}</p></div>
	</div>
{:else if services.length === 0}
	<div class="table-wrap">
		<div class="empty">
			<div class="empty-icon">⬡</div>
			<p>No services exposed yet.</p>
			<p style="margin-top:0.5rem">Add a <span class="mono" style="font-size:0.85em">zetafile.yml</span> to an agent to expose services on the mesh.</p>
		</div>
	</div>
{:else}
	<div style="display:flex;flex-direction:column;gap:1rem">
		{#each services as svc}
			<div class="card">
				<div style="display:flex;align-items:flex-start;justify-content:space-between;gap:1rem;flex-wrap:wrap">
					<div style="display:flex;align-items:center;gap:0.75rem">
						<div>
							<div style="font-size:1rem;font-weight:700;color:var(--accent)">{svc.name}</div>
							<div class="mono" style="font-size:0.78rem;color:var(--text);margin-top:0.15rem">{domain(svc)}</div>
						</div>
					</div>
					<span class="badge {hostOnline[svc.device_hostname] ? 'badge-online' : 'badge-offline'}">
						<span class="badge-dot"></span>
						{hostOnline[svc.device_hostname] ? 'host online' : 'host offline'}
					</span>
				</div>

				<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:0.75rem;margin-top:1rem">
					<div class="info-block">
						<div class="info-label">Host</div>
						<div class="info-val mono">{svc.device_hostname}</div>
					</div>
					<div class="info-block">
						<div class="info-label">Mesh Address</div>
						<div class="info-val mono">{svc.device_mesh_ip}:{svc.port}</div>
					</div>
					<div class="info-block">
						<div class="info-label">Target</div>
						<div class="info-val mono">{svc.target_addr || '—'}</div>
					</div>
					<div class="info-block">
						<div class="info-label">Port</div>
						<div class="info-val mono">{svc.port}</div>
					</div>
				</div>

				<div style="margin-top:1rem">
					<div class="info-label" style="margin-bottom:0.4rem">Access</div>
					{#if !svc.allowed_pubkeys || svc.allowed_pubkeys.length === 0}
						<span class="badge badge-offline">no access granted</span>
					{:else}
						<div style="display:flex;flex-wrap:wrap;gap:0.4rem">
							{#each svc.allowed_pubkeys as pk}
								<span class="badge {hostOnline[pkToHostname[pk]] ? 'badge-online' : 'badge-offline'}" style="font-size:0.75rem">
									<span class="badge-dot"></span>
									{pkToHostname[pk] ?? pk.slice(0, 12) + '…'}
								</span>
							{/each}
						</div>
					{/if}
				</div>
			</div>
		{/each}
	</div>
{/if}

<style>
	.info-block {
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
	}
	.info-label {
		font-size: 0.72rem;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text);
		opacity: 0.7;
	}
	.info-val {
		font-size: 0.85rem;
		color: var(--accent);
	}
</style>
