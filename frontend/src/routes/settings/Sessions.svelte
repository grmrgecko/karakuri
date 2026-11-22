<script lang="ts">
  import { onMount } from 'svelte';
  import { sessionsStore } from '../../lib/stores/sessions';
  import type { Session } from '../../lib/stores/sessions';
  import { confirm } from '../../lib/stores/confirmDialog';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  let sessions: Session[] = $state([]);
  let loading = $state(true);
  let error = $state('');

  onMount(() => {
    loadSessions();
  });

  async function loadSessions() {
    loading = true;
    error = '';
    try {
      sessions = await sessionsStore.list();
    } catch (e: any) {
      error = e.message || 'Failed to load sessions';
    } finally {
      loading = false;
    }
  }

  async function revokeSession(id: string) {
    if (!await confirm('Revoke this session? The device will be logged out immediately.')) return;
    error = '';
    try {
      await sessionsStore.revoke(id);
      sessions = sessions.filter((s) => s.id !== id);
    } catch (e: any) {
      error = e.message || 'Failed to revoke session';
    }
  }

  async function revokeOthers() {
    if (!await confirm('Revoke all other sessions? All other devices will be logged out.')) return;
    error = '';
    try {
      await sessionsStore.revokeOthers();
      sessions = sessions.filter((s) => s.current);
    } catch (e: any) {
      error = e.message || 'Failed to revoke sessions';
    }
  }

  function formatDate(dateStr: string): string {
    return new Date(dateStr).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  }

  // Full agent strings blow out the column; the title attribute keeps the rest.
  function shortenUserAgent(ua: string): string {
    if (ua.length <= 60) return ua;
    return ua.substring(0, 57) + '...';
  }

  let hasOtherSessions = $derived(sessions.some((s) => !s.current));
</script>

<PageHeader title="Active Sessions">
  {#if hasOtherSessions}
    <button class="btn btn-danger text-sm" onclick={revokeOthers}>
      <i class="las la-sign-out-alt mr-1"></i> Revoke All Others
    </button>
  {/if}
</PageHeader>

<p class="text-sm mb-6 opacity-60">
  These are the devices currently logged into your account. Revoke any session you don't recognize.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

{#if loading}
  <p class="opacity-60">Loading sessions...</p>
{:else if sessions.length === 0}
  <p class="opacity-60 text-center py-8">No active sessions.</p>
{:else}
  <div class="card overflow-hidden">
    <table class="w-full text-sm">
      <thead>
        <tr style="background-color: var(--theme-surface0);">
          <th class="text-left px-4 py-2">IP Address</th>
          <th class="text-left px-4 py-2">User Agent</th>
          <th class="text-left px-4 py-2">Last Active</th>
          <th class="text-left px-4 py-2">Created</th>
          <th class="text-right px-4 py-2">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each sessions as session (session.id)}
          <tr class="border-t" style="border-color: var(--theme-surface1);">
            <td class="px-4 py-2 font-mono text-xs">{session.ip_address}</td>
            <td class="px-4 py-2 text-xs" title={session.user_agent}>
              {shortenUserAgent(session.user_agent)}
            </td>
            <td class="px-4 py-2 text-xs opacity-60">{formatDate(session.last_active_at)}</td>
            <td class="px-4 py-2 text-xs opacity-60">{formatDate(session.created_at)}</td>
            <td class="px-4 py-2 text-right">
              {#if session.current}
                <span class="current-badge">current</span>
              {:else}
                <button class="btn btn-danger text-xs px-2 py-1" onclick={() => revokeSession(session.id)}>
                  Revoke
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .current-badge {
    display: inline-block;
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    background-color: var(--theme-success, #a6e3a1);
    color: var(--theme-base, #1e1e2e);
  }
</style>
