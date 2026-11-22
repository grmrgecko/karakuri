<script lang="ts">
  import { copyText } from '../../lib/utils/copyText';
  import { onMount } from 'svelte';
  import { link } from 'svelte-routing';
  import { api } from '../../lib/api/client';
  import { hasPermission } from '../../lib/stores/auth';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  // releasesURL is where the agent archives and container image are published.
  const releasesURL = 'https://github.com/grmrgecko/karakuri/releases';

  interface Agent {
    id: string;
    name: string;
    description?: string;
    hostname?: string;
    os?: string;
    arch?: string;
    version?: string;
    enabled: boolean;
    connected: boolean;
    last_seen_at?: string;
    cert_expires_at?: string;
    created_at: string;
    // access is the level the caller holds: use, edit, or admin.
    access?: 'use' | 'edit' | 'admin';
  }

  interface CAStatus {
    fingerprint: string;
    expires_at: string;
    rotating: boolean;
    roots: number;
    server_cert_expires_at: string;
  }

  interface EnrollToken {
    id: string;
    name?: string;
    description?: string;
    expires_at: string;
    used_at?: string;
    created_at: string;
  }

  interface MintedToken {
    token: string;
    server?: string;
    command?: string;
    expires_at: string;
  }

  let agents = $state<Agent[]>([]);
  let tokens = $state<EnrollToken[]>([]);
  let caStatus = $state<CAStatus | null>(null);
  let loading = $state(true);
  let error = $state('');
  let busy = $state('');

  const canWrite = hasPermission('api:agents:write');
  const canEnroll = hasPermission('api:agents:enroll');
  const canRevoke = hasPermission('api:agents:revoke');

  // Token minting.
  let tokenName = $state('');
  let tokenDesc = $state('');
  let minted = $state<MintedToken | null>(null);
  let copied = $state(false);

  onMount(load);

  async function load() {
    loading = true;
    error = '';
    try {
      agents = await api.agents.list<Agent[]>();
      tokens = await api.agents.tokens<EnrollToken[]>();
      caStatus = await api.agents.ca<CAStatus>();
    } catch (e: any) {
      error = e.message || 'Failed to load agents';
    }
    loading = false;
  }

  async function toggleEnabled(a: Agent) {
    await save(a, { name: a.name, description: a.description || '', enabled: !a.enabled });
  }

  async function save(a: Agent, body: { name: string; description: string; enabled: boolean }) {
    busy = a.id;
    error = '';
    try {
      await api.agents.update(a.id, body);
      await load();
    } catch (e: any) {
      error = e.message || 'Update failed';
    }
    busy = '';
  }

  async function revoke(a: Agent) {
    if (!confirm(`Revoke ${a.name}? Its certificate stops working and it is disabled.`)) return;
    busy = a.id;
    error = '';
    try {
      await api.agents.revoke(a.id);
      await load();
    } catch (e: any) {
      error = e.message || 'Revoke failed';
    }
    busy = '';
  }

  async function remove(a: Agent) {
    if (!confirm(`Delete ${a.name}? Its certificate is revoked and the record removed.`)) return;
    busy = a.id;
    error = '';
    try {
      await api.agents.remove(a.id);
      await load();
    } catch (e: any) {
      error = e.message || 'Delete failed';
    }
    busy = '';
  }

  async function mint() {
    error = '';
    copied = false;
    try {
      minted = await api.agents.mintToken<MintedToken>({ name: tokenName.trim(), description: tokenDesc.trim() });
      tokenName = '';
      tokenDesc = '';
      await load();
    } catch (e: any) {
      error = e.message || 'Could not mint a token';
    }
  }

  async function deleteToken(id: string) {
    try {
      await api.agents.deleteToken(id);
      await load();
    } catch (e: any) {
      error = e.message || 'Could not delete the token';
    }
  }

  async function rotateCA() {
    if (
      !confirm(
        'Start a CA rollover? A new root is generated now; agents move to it as they renew, ' +
          'and it takes over once they all have.',
      )
    )
      return;
    busy = 'ca';
    error = '';
    try {
      await api.agents.rotateCA();
      await load();
    } catch (e: any) {
      error = e.message || 'Could not start the rollover';
    }
    busy = '';
  }

  // A certificate renews at a third of its life left, so the number that
  // matters to an operator is days, not a timestamp.
  function daysUntil(when?: string): number | null {
    if (!when) return null;
    const ms = new Date(when).getTime() - Date.now();
    return Math.floor(ms / 86400000);
  }

  function certNote(a: Agent): string {
    const days = daysUntil(a.cert_expires_at);
    if (days === null) return '';
    if (days < 0) return ' · certificate expired, re-enroll';
    return ` · certificate ${days}d left`;
  }

  async function copyCommand() {
    if (!minted?.command) return;
    copied = await copyText(minted.command);
  }

  function statusColor(a: Agent): string {
    if (!a.enabled) return 'var(--theme-subtext0, #a6adc8)';
    return a.connected ? 'var(--theme-green, #a6e3a1)' : 'var(--theme-yellow, #f9e2af)';
  }

  function statusLabel(a: Agent): string {
    if (!a.enabled) return 'disabled';
    return a.connected ? 'connected' : 'offline';
  }

  function seen(a: Agent): string {
    if (!a.last_seen_at) return 'never';
    return new Date(a.last_seen_at).toLocaleString();
  }

  function pending(t: EnrollToken): boolean {
    return !t.used_at && new Date(t.expires_at) > new Date();
  }
</script>

<PageHeader title="Agents" />

<p class="text-sm opacity-60 mb-4 max-w-2xl">
  Agents are remote hosts that run real scripts for karakuri. Enroll one with a token, then it dials
  in and its nodes appear in the flow editor under its name. They dial
  <code>agents.listen</code>, port 9443 on every interface unless the server config says
  otherwise.
</p>

{#if error}
  <p class="mb-4 text-sm" style="color: var(--theme-red, #f38ba8)">{error}</p>
{/if}

{#if caStatus}
  <div class="card p-4 mb-6 flex flex-wrap items-center gap-x-6 gap-y-2">
    <div class="text-sm">
      <span class="opacity-60">Agent CA</span>
      · expires {new Date(caStatus.expires_at).toLocaleDateString()}
      <span class="opacity-40">({daysUntil(caStatus.expires_at)}d)</span>
    </div>
    <div class="text-sm">
      <span class="opacity-60">Listener certificate</span>
      · expires {new Date(caStatus.server_cert_expires_at).toLocaleDateString()}
      <span class="opacity-40">(renews itself)</span>
    </div>
    {#if caStatus.rotating}
      <div class="text-sm" style="color: var(--theme-yellow, #f9e2af)">
        Rollover in progress · {caStatus.roots} roots trusted · the new one takes over once every
        agent has renewed
      </div>
    {/if}
    {#if canRevoke}
      <button class="btn-sm ml-auto" disabled={busy === 'ca'} onclick={rotateCA}>Rotate CA</button>
    {/if}
  </div>
{/if}

<div class="card p-4 mb-6 text-sm">
  <div class="font-semibold mb-2">Get the agent</div>
  <p class="mb-2 max-w-3xl">
    Download the agent from
    <a href={releasesURL} target="_blank" rel="noopener noreferrer" class="link">GitHub releases</a>,
    using the same release as this server. Each release has an agent archive per platform, named
    <code>karakuri-agent-&lt;version&gt;.&lt;os&gt;-&lt;arch&gt;</code>: Linux, macOS, Windows, and
    FreeBSD on amd64 and arm64, and 32-bit ARM Linux as <code>linux-armv7</code>. Windows archives
    are <code>.zip</code>, the rest <code>.tar.gz</code>. Each holds one binary with nothing else
    to install.
  </p>
  <p class="mb-1 opacity-75">
    On the host, install it for the user the agent should act as, into your own binary directory:
  </p>
  <code class="block text-xs overflow-x-auto whitespace-pre p-2 rounded mb-2"
        style="background: var(--theme-crust, #11111b)">mkdir -p ~/.local/bin
tar xzf karakuri-agent-&lt;version&gt;.linux-amd64.tar.gz
install karakuri-agent-&lt;version&gt;.linux-amd64/karakuri-agent ~/.local/bin/</code>
  <p class="mb-2 max-w-3xl">
    Then mint a token below and run the command it gives you there, as yourself and without
    <code>sudo</code>. It enrolls the agent and starts it as a service for your account, which is
    what lets it reach your desktop and update itself. On Windows, extract the <code>.zip</code>
    into <code>%LOCALAPPDATA%\Programs\karakuri-agent</code>. On macOS, a binary from a browser
    download needs <code>xattr -dr com.apple.quarantine</code> on the unpacked directory before it
    will start. To run the agent in a container instead, use the
    <code>grmrgecko/karakuri-agent</code> image with <code>KARAKURI_SERVER</code> and
    <code>KARAKURI_JOIN_TOKEN</code> set.
  </p>
  <p class="mb-2 max-w-3xl opacity-75">
    Installing as root runs every command as root and lets flows drive whoever is signed in at the
    desktop. If you need it anyway, install to <code>/usr/local/bin</code> and run both the enroll
    command and service commands with <code>sudo</code>.
  </p>
  <a href="/help/agent-nodes#installing-an-agent" use:link class="link">
    Installing an agent: root installs, services, settings, and updates
  </a>
</div>

{#if canEnroll}
  <div class="card p-4 mb-6">
    <div class="font-semibold mb-2">Enroll an agent</div>
    <div class="flex flex-wrap gap-2 items-end">
      <label class="text-sm">
        <span class="block opacity-60 mb-1">Name (optional)</span>
        <input class="input" bind:value={tokenName} placeholder="hostname" />
      </label>
      <label class="text-sm flex-1 min-w-[12rem]">
        <span class="block opacity-60 mb-1">Description (optional)</span>
        <input class="input w-full" bind:value={tokenDesc} placeholder="e.g. NAS in the garage" />
      </label>
      <button class="btn" onclick={mint}>Mint token</button>
    </div>

    {#if minted}
      <div class="mt-4 p-3 rounded" style="background: var(--theme-mantle, #181825)">
        <div class="text-sm mb-1">
          Run this on the host as the user the agent should act as. The token carries this
          server's CA, so the agent verifies what it dials with nothing installed first. It is
          single-use and expires
          {new Date(minted.expires_at).toLocaleString()}.
        </div>
        <div class="flex items-center gap-2">
          <code class="text-xs flex-1 overflow-x-auto whitespace-pre p-2 rounded"
                style="background: var(--theme-crust, #11111b)">{minted.command}</code>
          <button class="btn-sm" onclick={copyCommand}>{copied ? 'Copied' : 'Copy'}</button>
        </div>
        <div class="text-xs mt-2 opacity-60">
          Check the address: it is a guess from <code>agents.listen</code> and
          <code>server.base_url</code>, and nothing verifies against it. Any address that reaches
          this server works.
        </div>
      </div>
    {/if}

    {#if tokens.filter(pending).length > 0}
      <div class="mt-4">
        <div class="text-xs opacity-60 mb-1">Pending tokens</div>
        {#each tokens.filter(pending) as t (t.id)}
          <div class="flex items-center gap-2 text-sm py-1">
            <span class="flex-1">{t.name || '(unnamed)'} · expires {new Date(t.expires_at).toLocaleString()}</span>
            <button class="btn-sm" onclick={() => deleteToken(t.id)}>Revoke</button>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/if}

{#if loading && agents.length === 0}
  <p class="opacity-60">Loading agents...</p>
{:else if agents.length === 0}
  <p class="opacity-60">No agents enrolled yet.</p>
{:else}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    {#each agents as a (a.id)}
      <div class="card p-4">
        <div class="flex items-start justify-between gap-3 mb-2">
          <div class="flex-1 min-w-0">
            <div class="font-semibold flex items-center gap-2 flex-wrap">
              {a.name}
              <span class="text-xs font-medium px-2 py-0.5 rounded"
                    style="color: {statusColor(a)}; border: 1px solid {statusColor(a)}">
                {statusLabel(a)}
              </span>
            </div>
            {#if a.description}
              <div class="text-sm opacity-60 mt-1">{a.description}</div>
            {/if}
            <div class="text-xs opacity-50 mt-2">
              {[a.os, a.arch].filter(Boolean).join('/') || 'unknown platform'}
              {#if a.hostname} · {a.hostname}{/if}
              · last seen {seen(a)}{certNote(a)}
            </div>
          </div>
          {#if a.version}
            <div class="text-xs opacity-30 flex-shrink-0">v{a.version}</div>
          {/if}
        </div>

        {#if a.access !== 'use'}
          <div class="flex flex-wrap gap-2 mt-3">
            {#if canWrite}
              <button class="btn-sm" disabled={busy === a.id} onclick={() => toggleEnabled(a)}>
                {a.enabled ? 'Disable' : 'Enable'}
              </button>
            {/if}
            {#if a.access === 'admin'}
              <a class="btn-sm" href="/settings/agents/{a.id}/share" use:link>Share</a>
              {#if canRevoke}
                <button class="btn-sm" disabled={busy === a.id} onclick={() => revoke(a)}>Revoke</button>
              {/if}
              {#if canWrite}
                <button class="btn-sm" disabled={busy === a.id} onclick={() => remove(a)}>Delete</button>
              {/if}
            {/if}
          </div>
        {:else}
          <div class="text-xs opacity-50 mt-3">Shared with you to use in flows.</div>
        {/if}
      </div>
    {/each}
  </div>
{/if}

<style>
  .input {
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 0.375rem;
    padding: 0.375rem 0.5rem;
    color: inherit;
  }
  .btn {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-crust, #11111b);
    border-radius: 0.375rem;
    padding: 0.375rem 0.75rem;
    font-weight: 500;
  }
  .btn-sm {
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 0.375rem;
    padding: 0.25rem 0.625rem;
    font-size: 0.8125rem;
  }
  .btn-sm:disabled {
    opacity: 0.5;
  }
</style>
