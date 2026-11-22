<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import { confirm } from '../../lib/stores/confirmDialog';
  import { hasPermission } from '../../lib/stores/auth';
  import { formatBytes } from '../../lib/stores/history';
  import PageHeader from '../../lib/components/PageHeader.svelte';
  import FormField from '../../lib/components/FormField.svelte';

  interface Run {
    id: number;
    started_at: string;
    finished_at?: string;
    trigger: string;
    archive?: string;
    size: number;
    status: 'running' | 'succeeded' | 'failed';
    error?: string;
    verified: string;
  }
  interface Archive {
    name: string;
    size: number;
    created_at: string;
    verified: string;
    counts?: Record<string, number>;
  }
  interface Listing {
    directory: string;
    archives: Archive[];
    running?: Run;
  }
  interface Schedule {
    enabled: boolean;
    cron: string;
    description: string;
    directory: string;
    retain: number;
    passphrase_set: boolean;
    next_at?: string;
  }

  const canWrite = hasPermission('api:backups:write');
  const canDownload = hasPermission('api:backups:download');

  let listing: Listing = $state({ directory: '', archives: [] });
  let runs: Run[] = $state([]);
  let schedule: Schedule = $state({ enabled: false, cron: '', description: '', directory: '', retain: 14, passphrase_set: false });
  let form = $state({ enabled: false, cron: '', directory: '', retain: 14, passphrase: '' });
  let loading = $state(true);
  let error = $state('');
  let notice = $state('');
  let saving = $state(false);
  let verifying = $state('');
  let poll: ReturnType<typeof setInterval> | undefined;

  onMount(() => {
    load(true);
  });
  onDestroy(() => clearInterval(poll));

  async function load(resetForm = false) {
    try {
      const [l, r, s] = await Promise.all([
        api.get<Listing>('/backups'),
        api.get<Run[]>('/backups/runs?limit=10'),
        api.get<Schedule>('/backups/schedule'),
      ]);
      listing = l;
      runs = r ?? [];
      schedule = s;
      if (resetForm) {
        form = { enabled: s.enabled, cron: s.cron, directory: s.directory, retain: s.retain, passphrase: '' };
      }
      // While a backup runs, follow it until it finishes.
      clearInterval(poll);
      if (l.running) poll = setInterval(() => load(), 2000);
    } catch (e: any) {
      error = e.message || 'the backups could not be read';
    } finally {
      loading = false;
    }
  }

  async function saveSchedule() {
    saving = true;
    error = '';
    notice = '';
    try {
      const body: Record<string, unknown> = {
        enabled: form.enabled,
        cron: form.cron,
        directory: form.directory,
        retain: Number(form.retain),
      };
      if (form.passphrase !== '') body.passphrase = form.passphrase;
      schedule = await api.put<Schedule>('/backups/schedule', body);
      form.passphrase = '';
      notice = 'Schedule saved';
    } catch (e: any) {
      error = e.message || 'the schedule could not be saved';
    } finally {
      saving = false;
    }
  }

  async function backUpNow() {
    error = '';
    notice = '';
    try {
      await api.post('/backups', {});
      notice = 'Backup started';
      await load();
    } catch (e: any) {
      error = e.message || 'the backup could not start';
    }
  }

  async function verify(a: Archive) {
    verifying = a.name;
    error = '';
    notice = '';
    try {
      await api.post(`/backups/${encodeURIComponent(a.name)}/verify`, {});
      notice = `${a.name} is complete and every part matches`;
    } catch (e: any) {
      error = e.message || 'the archive could not be verified';
    } finally {
      verifying = '';
    }
  }

  async function download(a: Archive) {
    error = '';
    try {
      const url = await api.fetchFile(`/api/backups/${encodeURIComponent(a.name)}/download`);
      const link = document.createElement('a');
      link.href = url;
      link.download = a.name;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
    } catch (e: any) {
      error = e.message || 'the archive could not be downloaded';
    }
  }

  async function remove(a: Archive) {
    if (!await confirm(`Delete ${a.name}? The file is removed from the backup directory.`)) return;
    error = '';
    try {
      await api.delete(`/backups/${encodeURIComponent(a.name)}`);
      await load();
    } catch (e: any) {
      error = e.message || 'the archive could not be deleted';
    }
  }

  function when(s?: string): string {
    if (!s) return '';
    return new Date(s).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  function duration(r: Run): string {
    if (!r.finished_at) return '';
    const s = Math.round((new Date(r.finished_at).getTime() - new Date(r.started_at).getTime()) / 1000);
    return s < 60 ? `${s} s` : `${Math.floor(s / 60)} min ${s % 60} s`;
  }
</script>

<PageHeader title="Backup">
  {#if canWrite}
    <button class="btn btn-primary text-sm" onclick={backUpNow} disabled={!!listing.running || !schedule.passphrase_set}
            title={schedule.passphrase_set ? 'Write a backup with the stored passphrase' : 'Store a passphrase below first'}>
      <i class="las la-archive mr-1"></i> Back up now
    </button>
  {/if}
</PageHeader>

<p class="text-sm mb-6 opacity-60">
  A backup is one encrypted file holding the database, the encryption key, enrolled agents' identity, uploads, and flow
  data. Restore it with <code>karakuri backup restore</code> while the server is stopped.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{error}</div>
{/if}
{#if notice}
  <div class="mb-4 p-3 rounded text-sm" style="background-color: var(--theme-surface0);">{notice}</div>
{/if}

{#if loading}
  <p class="opacity-60">Loading backups...</p>
{:else}
  <section class="card p-4 mb-6">
    <h2 class="section-title">Schedule</h2>
    <div class="grid">
      <label class="toggle">
        <input type="checkbox" bind:checked={form.enabled} disabled={!canWrite} />
        <span>Back up on a schedule</span>
      </label>
      <FormField label="Schedule" id="backup-cron">
        <input id="backup-cron" class="input font-mono" placeholder="0 3 * * *" bind:value={form.cron} disabled={!canWrite} />
        <p class="hint">
          {schedule.description}{#if schedule.next_at}. Next {when(schedule.next_at)}{/if}
        </p>
      </FormField>
      <FormField label="Directory" id="backup-dir">
        <input id="backup-dir" class="input font-mono" bind:value={form.directory} disabled={!canWrite} />
        <p class="hint">Another disk keeps backups safe from this one failing.</p>
      </FormField>
      <FormField label="Keep" id="backup-retain">
        <input id="backup-retain" class="input" type="number" min="1" max="365" bind:value={form.retain} disabled={!canWrite} />
        <p class="hint">Older scheduled archives are deleted once a new one verifies.</p>
      </FormField>
      <FormField label={schedule.passphrase_set ? 'Replace passphrase' : 'Passphrase'} id="backup-pass">
        <input id="backup-pass" class="input" type="password" autocomplete="new-password" minlength="12"
               placeholder={schedule.passphrase_set ? 'Stored; leave empty to keep it' : 'At least 12 characters'}
               bind:value={form.passphrase} disabled={!canWrite} />
        <p class="hint">A lost passphrase cannot be recovered. Keep a copy somewhere other than this server.</p>
      </FormField>
    </div>
    {#if canWrite}
      <div class="mt-4">
        <button class="btn btn-primary text-sm" onclick={saveSchedule} disabled={saving}>Save schedule</button>
      </div>
    {/if}
  </section>

  <section class="card overflow-hidden mb-6">
    <h2 class="section-title px-4 pt-4">Archives</h2>
    <p class="text-xs opacity-60 px-4 pb-2 font-mono">{listing.directory}</p>
    {#if listing.running}
      <p class="px-4 pb-3 text-sm"><i class="las la-spinner la-spin mr-1"></i> Backing up since {when(listing.running.started_at)}</p>
    {/if}
    {#if listing.archives.length === 0}
      <p class="opacity-60 text-center py-8 text-sm">No archives yet. Back up now, or turn on the schedule.</p>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr style="background-color: var(--theme-surface0);">
            <th class="text-left px-4 py-2">Archive</th>
            <th class="text-left px-4 py-2">Taken</th>
            <th class="text-right px-4 py-2">Size</th>
            <th class="text-left px-4 py-2">Verified</th>
            <th class="text-right px-4 py-2">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each listing.archives as a (a.name)}
            <tr class="border-t" style="border-color: var(--theme-surface1);">
              <td class="px-4 py-2 font-mono text-xs break-all">{a.name}</td>
              <td class="px-4 py-2 text-xs opacity-80">{when(a.created_at)}</td>
              <td class="px-4 py-2 text-xs text-right">{formatBytes(a.size)}</td>
              <td class="px-4 py-2 text-xs">
                <span class="badge" class:ok={a.verified === 'ok'} class:bad={a.verified === 'failed'}>{a.verified}</span>
              </td>
              <td class="px-4 py-2 text-right whitespace-nowrap">
                {#if canWrite}
                  <button class="btn text-xs px-2 py-1" onclick={() => verify(a)} disabled={verifying !== ''}>
                    {verifying === a.name ? 'Verifying...' : 'Verify'}
                  </button>
                {/if}
                {#if canDownload}
                  <button class="btn text-xs px-2 py-1" onclick={() => download(a)}>Download</button>
                {/if}
                {#if canWrite}
                  <button class="btn btn-danger text-xs px-2 py-1" onclick={() => remove(a)}>Delete</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>

  <section class="card overflow-hidden">
    <h2 class="section-title px-4 pt-4 pb-2">Recent backups</h2>
    {#if runs.length === 0}
      <p class="opacity-60 text-center py-6 text-sm">No backup has run yet.</p>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr style="background-color: var(--theme-surface0);">
            <th class="text-left px-4 py-2">Started</th>
            <th class="text-left px-4 py-2">Trigger</th>
            <th class="text-left px-4 py-2">Result</th>
            <th class="text-left px-4 py-2">Took</th>
          </tr>
        </thead>
        <tbody>
          {#each runs as r (r.id)}
            <tr class="border-t" style="border-color: var(--theme-surface1);">
              <td class="px-4 py-2 text-xs">{when(r.started_at)}</td>
              <td class="px-4 py-2 text-xs">{r.trigger}</td>
              <td class="px-4 py-2 text-xs">
                <span class="badge" class:ok={r.status === 'succeeded'} class:bad={r.status === 'failed'}>{r.status}</span>
                {#if r.error}<span class="opacity-70 ml-1">{r.error}</span>{/if}
              </td>
              <td class="px-4 py-2 text-xs opacity-80">{duration(r)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{/if}

<style>
  .section-title {
    font-weight: 600;
    margin-bottom: 0.5rem;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
    gap: 1rem;
  }
  .toggle {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    grid-column: 1 / -1;
  }
  .input {
    width: 100%;
  }
  .hint {
    font-size: 0.75rem;
    opacity: 0.6;
    margin-top: 0.25rem;
  }
  .badge {
    display: inline-block;
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    background-color: var(--theme-surface1);
  }
  .badge.ok {
    background-color: var(--theme-success, #a6e3a1);
    color: var(--theme-base, #1e1e2e);
  }
  .badge.bad {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }
</style>
