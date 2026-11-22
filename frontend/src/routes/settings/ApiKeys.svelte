<script lang="ts">
  import { copyText } from '../../lib/utils/copyText';
  import { onMount } from 'svelte';
  import { apiKeyStore } from '../../lib/stores/apiKeys';
  import { confirm } from '../../lib/stores/confirmDialog';
  import type { APIKey } from '../../lib/stores/apiKeys';
  import FormField from '../../lib/components/FormField.svelte';
  import PermissionList from '../../lib/components/PermissionList.svelte';
  import { treeFromPermissions } from '../../lib/permissionTree';

  let keys: APIKey[] = $state([]);
  let loading = $state(true);
  let error = $state('');

  // Create form state.
  let showCreateForm = $state(false);
  let newKeyName = $state('');
  let newKeyExpiry = $state('');
  let creating = $state(false);

  // Limiting a key: the permissions typed in complete against the ones the
  // caller holds, the same way a role's permissions complete against the
  // registry.
  let limitKey = $state(false);
  let available: string[] = $state([]);
  let availableLoaded = $state(false);
  let chosen: string[] = $state([]);
  let tree = $derived(treeFromPermissions(available));

  // Displayed once after creation.
  let createdKey = $state('');

  async function loadAvailable() {
    if (availableLoaded) return;
    try {
      available = await apiKeyStore.permissions();
      availableLoaded = true;
    } catch (e: any) {
      error = e.message || 'Failed to load the permissions a key may hold';
    }
  }

  function resetForm() {
    newKeyName = '';
    newKeyExpiry = '';
    limitKey = false;
    chosen = [];
  }

  onMount(async () => {
    await loadKeys();
  });

  async function loadKeys() {
    loading = true;
    error = '';
    try {
      keys = await apiKeyStore.list();
    } catch (e: any) {
      error = e.message || 'Failed to load API keys';
    } finally {
      loading = false;
    }
  }

  async function createKey() {
    if (!newKeyName.trim()) return;
    creating = true;
    error = '';
    createdKey = '';
    try {
      const expiresAt = newKeyExpiry
        ? new Date(newKeyExpiry).toISOString()
        : undefined;
      const result = await apiKeyStore.create({
        name: newKeyName.trim(),
        expiresAt,
        permissions: limitKey ? chosen : undefined,
      });
      createdKey = result.key;
      resetForm();
      showCreateForm = false;
      await loadKeys();
    } catch (e: any) {
      error = e.message || 'Failed to create API key';
    } finally {
      creating = false;
    }
  }

  async function deleteKey(id: number) {
    if (!await confirm('Delete this API key? Any integrations using it will stop working.')) return;
    error = '';
    try {
      await apiKeyStore.remove(id);
      keys = keys.filter((k) => k.id !== id);
    } catch (e: any) {
      error = e.message || 'Failed to delete API key';
    }
  }

  // expiryPresets set the date field relative to today; Never clears it.
  const expiryPresets: { label: string; days?: number; years?: number }[] = [
    { label: 'In 30 days', days: 30 },
    { label: 'In 90 days', days: 90 },
    { label: 'In a year', years: 1 },
    { label: 'Never' },
  ];

  // applyExpiryPreset formats the local calendar date because the date input
  // takes YYYY-MM-DD and toISOString would shift it across UTC midnight.
  function applyExpiryPreset(preset: { days?: number; years?: number }) {
    if (!preset.days && !preset.years) {
      newKeyExpiry = '';
      return;
    }
    const d = new Date();
    if (preset.days) d.setDate(d.getDate() + preset.days);
    if (preset.years) d.setFullYear(d.getFullYear() + preset.years);
    const pad = (n: number) => String(n).padStart(2, '0');
    newKeyExpiry = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  }

  let keyCopied = $state(false);

  async function copyToClipboard(text: string) {
    keyCopied = await copyText(text);
  }

  function formatDate(dateStr: string | null): string {
    if (!dateStr) return '-';
    return new Date(dateStr).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  }

  function isExpired(dateStr: string | null): boolean {
    if (!dateStr) return false;
    return new Date(dateStr) < new Date();
  }
</script>

<h2 class="text-xl font-bold mb-2">API Keys</h2>
<p class="text-sm mb-6" style="color: var(--theme-text-muted, #a6adc8)">
  API keys allow external tools to authenticate with Karakuri without using your session token.
  Keys are shown on creation and can be retrieved by an operator with server command access.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

{#if createdKey}
  <div class="card p-4 mb-4" style="border: 1px solid var(--theme-success, #a6e3a1);">
    <div class="flex items-center gap-2 mb-2">
      <i class="las la-check-circle" style="color: var(--theme-success, #a6e3a1); font-size: 1.2rem;"></i>
      <span class="font-semibold text-sm">API key created successfully</span>
    </div>
    <p class="text-xs mb-2" style="color: var(--theme-text-muted, #a6adc8)">
      Copy this key now or retrieve it later with the server token command.
    </p>
    <div class="flex gap-2">
      <input
        class="input w-full font-mono text-sm"
        value={createdKey}
        readonly
      />
      <button class="btn btn-primary" onclick={() => copyToClipboard(createdKey)} title={keyCopied ? 'Copied' : 'Copy'}>
        <i class="la {keyCopied ? 'la-check' : 'la-copy'}"></i>
      </button>
    </div>
  </div>
{/if}

<div class="flex justify-end mb-4">
  {#if !showCreateForm}
    <button class="btn btn-primary" onclick={() => { showCreateForm = true; createdKey = ''; }}>
      <i class="las la-plus mr-1"></i>Create Key
    </button>
  {/if}
</div>

{#if showCreateForm}
  <div class="card p-4 mb-4">
    <h3 class="font-semibold mb-3">New API Key</h3>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); createKey(); }} class="flex flex-col gap-3">
      <FormField label="Name" id="key-name">
        <input
          id="key-name"
          class="input w-full"
          bind:value={newKeyName}
          placeholder="e.g. Home Assistant integration"
          required
        />
      </FormField>
      <FormField label="Expiration (optional)" id="key-expiry">
        <input
          id="key-expiry"
          type="date"
          class="input w-full"
          bind:value={newKeyExpiry}
        />
        <div class="flex flex-wrap gap-2 mt-2">
          {#each expiryPresets as preset (preset.label)}
            <button type="button" class="btn btn-secondary btn-sm" onclick={() => applyExpiryPreset(preset)}>
              {preset.label}
            </button>
          {/each}
        </div>
      </FormField>
      <fieldset class="flex flex-col gap-2">
        <legend class="text-sm font-medium mb-1">Access</legend>
        <label class="flex items-center gap-2 text-sm">
          <input type="radio" name="key-access" checked={!limitKey} onchange={() => (limitKey = false)} />
          Everything my roles allow
        </label>
        <label class="flex items-center gap-2 text-sm">
          <input type="radio" name="key-access" checked={limitKey} onchange={() => { limitKey = true; void loadAvailable(); }} />
          Only the permissions I choose
        </label>
        {#if limitKey}
          <div class="perm-picker">
            <PermissionList
              permissions={chosen}
              {tree}
              onadd={(p) => { chosen = [...chosen, p]; }}
              onremove={(_p, i) => { chosen = chosen.filter((_, j) => j !== i); }}
              emptyText="Nothing added yet. A key with no permissions passes no permission check."
            />
            <p class="text-xs mt-2" style="color: var(--theme-text-muted, #a6adc8)">
              The key holds only these, and never more than your roles grant: if a role loses a
              permission, the key loses it too.
            </p>
          </div>
        {/if}
      </fieldset>
      <div class="flex gap-2 justify-end">
        <button type="button" class="btn btn-secondary" onclick={() => { showCreateForm = false; resetForm(); }}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={creating}>
          {creating ? 'Creating...' : 'Create'}
        </button>
      </div>
    </form>
  </div>
{/if}

{#if loading}
  <p class="opacity-60">Loading API keys...</p>
{:else if keys.length === 0}
  <p class="opacity-60 text-center py-8">No API keys created yet.</p>
{:else}
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
          <th class="py-2 pr-4 text-left">Name</th>
          <th class="py-2 pr-4 text-left">Access</th>
          <th class="py-2 pr-4 text-left">Prefix</th>
          <th class="py-2 pr-4 text-left">Created</th>
          <th class="py-2 pr-4 text-left">Last Used</th>
          <th class="py-2 pr-4 text-left">Expires</th>
          <th class="py-2 text-right">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each keys as key (key.id)}
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <td class="py-2 pr-4 font-medium">{key.name}</td>
            <td class="py-2 pr-4 text-xs">
              {#if key.dashboard_id}
                <span class="access-badge render" title="Opens only this dashboard's render URLs">
                  <i class="las la-image"></i> Render: {key.dashboard_name || 'deleted dashboard'}
                </span>
              {:else if key.limited}
                <details class="access-details">
                  <summary class="access-badge limited">
                    {(key.permissions ?? []).length} permission{(key.permissions ?? []).length === 1 ? '' : 's'}
                  </summary>
                  <ul>
                    {#each key.permissions ?? [] as p (p)}<li><code>{p}</code></li>{/each}
                  </ul>
                </details>
              {:else}
                <span class="access-badge">All my permissions</span>
              {/if}
            </td>
            <td class="py-2 pr-4 font-mono text-xs" style="color: var(--theme-text-muted, #a6adc8)">{key.prefix}...</td>
            <td class="py-2 pr-4 text-xs">{formatDate(key.created_at)}</td>
            <td class="py-2 pr-4 text-xs">{formatDate(key.last_used)}</td>
            <td class="py-2 pr-4 text-xs">
              {#if key.expires_at}
                <span class:expired={isExpired(key.expires_at)}>
                  {formatDate(key.expires_at)}
                  {#if isExpired(key.expires_at)}
                    <span class="expired-badge">expired</span>
                  {/if}
                </span>
              {:else}
                <span style="color: var(--theme-text-muted, #a6adc8)">Never</span>
              {/if}
            </td>
            <td class="py-2 text-right">
              <button
                class="icon-btn icon-btn-danger"
                title="Delete"
                onclick={() => deleteKey(key.id)}
              >
                <i class="la la-trash"></i>
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .perm-picker {
    padding: 0.6rem;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
  }

  .access-badge {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.1rem 0.4rem;
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
  }

  .access-badge.limited {
    cursor: pointer;
    color: var(--theme-warning, #f9e2af);
  }

  .access-badge.render {
    color: var(--theme-accent, #89b4fa);
  }

  .access-details ul {
    margin: 0.3rem 0 0;
    padding-left: 1rem;
  }

  .expired {
    color: var(--theme-error, #f38ba8);
  }

  .expired-badge {
    display: inline-block;
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    padding: 0.1rem 0.3rem;
    border-radius: 3px;
    margin-left: 0.25rem;
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }

  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    border: none;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    transition: background-color 0.15s ease;
  }

  .icon-btn:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .icon-btn-danger:hover {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }
</style>
