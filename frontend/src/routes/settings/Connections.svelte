<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {
    connectionsStore,
    type Connection,
    type ConnectionStatus,
  } from '../../lib/stores/connections';
  import { api } from '../../lib/api/client';
  import { link } from 'svelte-routing';
  import { websocket } from '../../lib/stores/websocket';
  import { confirm } from '../../lib/stores/confirmDialog';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import ConfigForm from '../../lib/components/editor/ConfigForm.svelte';
  import type { FieldSpec } from '../../lib/stores/nodeCatalog';

  // ConnectionKindItem mirrors the /connections/kinds response: registered
  // kinds with the config schema their settings form renders. The form below
  // is generic over this, so a plugin's kind appears with no change here.
  interface ConnectionKindItem {
    kind: string;
    label: string;
    config_spec: FieldSpec[];
    testable: boolean;
  }

  let loading = $state(true);
  let error = $state('');
  let connections = $derived($connectionsStore.connections);

  // Per-connection test state: 'running' or the result message.
  let testResults = $state<Record<string, { running: boolean; ok?: boolean; message?: string }>>({});

  // Create/edit modal state. The kind select switches which registered
  // schema the form renders; kind is fixed once a connection exists.
  let showModal = $state(false);
  let editing: Connection | null = $state(null);
  let saving = $state(false);
  let formError = $state('');
  let kinds = $state<ConnectionKindItem[]>([]);
  let form = $state({ name: '', kind: '' });
  let config = $state<Record<string, unknown>>({});
  let codexLogin = $state<'idle' | 'starting' | 'waiting' | 'complete' | 'error'>('idle');
  let codexLoginMessage = $state('');
  let codexLoginURL = $state('');
  let codexUserCode = $state('');
  let codexPollTimer: ReturnType<typeof setTimeout> | null = null;
  let showCodexImport = $state(false);
  let codexImportValue = $state('');
  let codexImportError = $state('');

  // AutoImportStatus mirrors /connections/{id}/auto_import: what the saved
  // connection's auto import has done since the server started.
  interface AutoImportStatus {
    enabled: boolean;
    remove_after?: string;
    imported: number;
    skipped: { key: string; name: string; reason: string }[];
    last_import_at?: string;
    last_removal_at?: string;
  }
  let importStatus = $state<AutoImportStatus | null>(null);

  async function loadImportStatus(id: string) {
    importStatus = null;
    try {
      const status = await api.connections.autoImport<AutoImportStatus>(id);
      if (editing?.id === id) importStatus = status ?? null;
    } catch {
      importStatus = null;
    }
  }
  const codexImportPlaceholder = '{"tokens":{"access_token":"...","refresh_token":"..."}}';

  let kindDef = $derived(kinds.find((k) => k.kind === form.kind));

  let unsubscribeWs: (() => void) | null = null;

  onMount(async () => {
    try {
      const [, kindList] = await Promise.all([
        connectionsStore.refresh(),
        api.connections.kinds<ConnectionKindItem[]>(),
      ]);
      kinds = kindList || [];
    } catch (e: any) {
      error = e.message || 'Failed to load connections';
    } finally {
      loading = false;
    }

    // Live status updates for every connection via wildcard topic.
    unsubscribeWs = websocket.subscribeTopic('connection:*:status', (data) => {
      const payload = data as { connection_id?: string; state?: string; error?: string; since?: string };
      if (payload?.connection_id && payload.state) {
        connectionsStore.applyStatus(payload.connection_id, {
          state: payload.state as ConnectionStatus['state'],
          error: payload.error,
          since: payload.since,
        });
      }
    });
  });

  onDestroy(() => {
    unsubscribeWs?.();
    stopCodexPolling();
  });

  function stopCodexPolling() {
    if (codexPollTimer !== null) clearTimeout(codexPollTimer);
    codexPollTimer = null;
  }

  function resetCodexLogin() {
    stopCodexPolling();
    codexLogin = config.chatgpt_auth ? 'complete' : 'idle';
    codexLoginMessage = '';
    codexLoginURL = '';
    codexUserCode = '';
  }

  // defaultConfig seeds a fresh config from a kind's declared defaults.
  function defaultConfig(kind: string): Record<string, unknown> {
    const def = kinds.find((k) => k.kind === kind);
    const cfg: Record<string, unknown> = {};
    for (const field of def?.config_spec ?? []) {
      if (field.default !== undefined) cfg[field.id] = field.default;
    }
    return cfg;
  }

  function openCreate() {
    editing = null;
    formError = '';
    form = { name: '', kind: kinds[0]?.kind ?? '' };
    config = defaultConfig(form.kind);
    resetCodexLogin();
    showModal = true;
  }

  function openEdit(conn: Connection) {
    editing = conn;
    formError = '';
    form = { name: conn.name, kind: conn.kind };
    // The backend sends the sentinel for stored secrets; keeping it on save
    // preserves the stored secret.
    config = { ...(conn.config || {}) };
    resetCodexLogin();
    importStatus = null;
    if (conn.config?.auto_import === true) loadImportStatus(conn.id);
    showModal = true;
  }

  function setConfigField(fieldId: string, value: unknown) {
    if (value === undefined) delete config[fieldId];
    else config[fieldId] = value;
    if ((fieldId === 'auth' && value !== 'chatgpt') || (fieldId === 'provider' && value !== 'openai')) {
      delete config.chatgpt_auth;
      resetCodexLogin();
    }
  }

  // switchKind resets the config to the new kind's defaults: nothing from one
  // driver's schema means anything to another's.
  function switchKind(kind: string) {
    form.kind = kind;
    config = defaultConfig(kind);
    resetCodexLogin();
  }

  function closeModal() {
    stopCodexPolling();
    showCodexImport = false;
    showModal = false;
  }

  function openCodexImport() {
    codexImportValue = '';
    codexImportError = '';
    showCodexImport = true;
  }

  function useImportedCodexCredentials() {
    const raw = codexImportValue.trim();
    try {
      const parsed = JSON.parse(raw);
      const tokens = parsed?.tokens ?? parsed;
      if (typeof tokens?.refresh_token !== 'string' || !tokens.refresh_token) {
        codexImportError = 'The file has no refresh_token';
        return;
      }
    } catch {
      codexImportError = 'The file is not valid JSON';
      return;
    }
    config.chatgpt_auth = raw;
    codexLogin = 'complete';
    codexLoginMessage = 'Credentials imported. Save the connection to verify them.';
    showCodexImport = false;
  }

  async function startCodexLogin() {
    stopCodexPolling();
    codexLogin = 'starting';
    codexLoginMessage = '';
    const loginWindow = window.open('about:blank', '_blank');
    // The provider's page is navigated into that tab; without this it could
    // steer this window through window.opener.
    if (loginWindow) loginWindow.opener = null;
    try {
      const login = await api.connections.startCodexLogin<{
        device_auth_id: string;
        user_code: string;
        verification_url: string;
        interval: number;
      }>();
      codexLoginURL = login.verification_url;
      codexUserCode = login.user_code;
      codexLogin = 'waiting';
      if (loginWindow) loginWindow.location.href = login.verification_url;
      codexLoginMessage = loginWindow
        ? 'Finish signing in with OpenAI in the new tab.'
        : 'Open the sign-in page and enter the code below.';
      scheduleCodexPoll(login.device_auth_id, login.user_code, login.interval);
    } catch (e: any) {
      loginWindow?.close();
      codexLogin = 'error';
      codexLoginMessage = e.message || 'Could not start OpenAI sign-in';
    }
  }

  function scheduleCodexPoll(deviceAuthId: string, userCode: string, interval: number) {
    codexPollTimer = setTimeout(async () => {
      try {
        const result = await api.connections.pollCodexLogin<{ pending: boolean; credentials?: string }>(
          deviceAuthId,
          userCode,
        );
        if (result.pending) {
          scheduleCodexPoll(deviceAuthId, userCode, interval);
          return;
        }
        if (!result.credentials) throw new Error('OpenAI returned no credentials');
        config.chatgpt_auth = result.credentials;
        codexLogin = 'complete';
        codexLoginMessage = 'Signed in with ChatGPT. Save the connection to keep this account.';
      } catch (e: any) {
        codexLogin = 'error';
        codexLoginMessage = e.message || 'OpenAI sign-in failed';
      }
    }, Math.max(interval, 5) * 1000 + 3000);
  }

  function buildConfig(): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(config)) {
      if (v === undefined || v === null || v === '') continue;
      out[k] = typeof v === 'string' ? v.trim() : v;
    }
    return out;
  }

  function validateForm(): string {
    if (!form.name.trim()) return 'Name is required';
    if (
      form.kind === 'ai' &&
      config.provider === 'openai' &&
      config.auth === 'chatgpt' &&
      !config.chatgpt_auth
    ) {
      return 'Sign in with ChatGPT before saving';
    }
    for (const field of kindDef?.config_spec ?? []) {
      if (!field.required) continue;
      const v = config[field.id];
      if (v === undefined || v === null || (typeof v === 'string' && v.trim() === '')) {
        return `${field.label} is required`;
      }
    }
    return '';
  }

  async function save() {
    formError = validateForm();
    if (formError) return;
    saving = true;
    try {
      let saved: Connection;
      if (editing) {
        saved = await connectionsStore.update(editing.id, { name: form.name.trim(), config: buildConfig() });
      } else {
        saved = await connectionsStore.create({ name: form.name.trim(), kind: form.kind, config: buildConfig() });
      }
      if (form.kind === 'ai' && config.provider === 'openai' && config.auth === 'chatgpt') {
        const result = await connectionsStore.test(saved.id);
        if (!result.ok) {
          editing = saved;
          config = { ...(saved.config || {}) };
          formError = `Connection saved, but ChatGPT authentication failed: ${result.error || 'OpenAI rejected the credentials'}`;
          return;
        }
      }
      closeModal();
    } catch (e: any) {
      formError = e.message || 'Failed to save connection';
    } finally {
      saving = false;
    }
  }

  async function toggleEnabled(conn: Connection) {
    error = '';
    try {
      await connectionsStore.update(conn.id, { enabled: !conn.enabled });
    } catch (e: any) {
      error = e.message || 'Failed to update connection';
    }
  }

  async function remove(conn: Connection) {
    if (!(await confirm(`Delete connection "${conn.name}"? Flows using it will stop working.`))) return;
    error = '';
    try {
      await connectionsStore.remove(conn.id);
    } catch (e: any) {
      error = e.message || 'Failed to delete connection';
    }
  }

  async function runTest(conn: Connection) {
    testResults[conn.id] = { running: true };
    try {
      const result = await connectionsStore.test(conn.id);
      testResults[conn.id] = {
        running: false,
        ok: result.ok,
        message: (result.ok ? 'Connection OK' : result.error || 'Connection failed') + (result.detail ? `: ${result.detail}` : ''),
      };
    } catch (e: any) {
      testResults[conn.id] = { running: false, ok: false, message: e.message || 'Test failed' };
    }
    // Clear the result toast after a few seconds; a result carrying what the
    // test found, such as calendar names to copy, stays long enough to read.
    const id = conn.id;
    const linger = testResults[id]?.message?.includes(': ') ? 20000 : 6000;
    setTimeout(() => {
      if (testResults[id] && !testResults[id].running) delete testResults[id];
    }, linger);
  }

  function statusColor(status: ConnectionStatus): string {
    switch (status?.state) {
      case 'connected':
        return 'var(--theme-success, #a6e3a1)';
      case 'connecting':
        return 'var(--theme-warning, #f9e2af)';
      case 'error':
        return 'var(--theme-error, #f38ba8)';
      default:
        return 'var(--theme-surface2, #585b70)';
    }
  }

  // canEdit reports whether the caller may change or test a connection; use
  // alone lists it and lets flows and dashboards reach it.
  function canEdit(conn: Connection): boolean {
    return conn.access === 'edit' || conn.access === 'admin';
  }

  // accessLabel names the caller's level on a connection for the table.
  function accessLabel(conn: Connection): string {
    if (conn.access === 'admin') return 'Admin';
    if (conn.access === 'edit') return 'Edit';
    return 'Use';
  }

  function statusLabel(status: ConnectionStatus): string {
    if (status?.state === 'error' && status.error) return `error: ${status.error}`;
    return status?.state || 'disabled';
  }
</script>

<h2 class="text-xl font-bold mb-2">Connections</h2>
<p class="text-sm mb-6" style="color: var(--theme-text-muted, #a6adc8)">
  Connections to external systems used by flow nodes, such as MQTT brokers and
  Home Assistant instances. Credentials are stored encrypted.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

<div class="flex justify-end mb-4">
  <button class="btn btn-primary" onclick={openCreate}>
    <i class="las la-plus mr-1"></i>Add Connection
  </button>
</div>

{#if loading}
  <p class="opacity-60">Loading connections...</p>
{:else if connections.length === 0}
  <p class="opacity-60 text-center py-8">No connections configured yet.</p>
{:else}
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
          <th class="py-2 pr-4 text-left">Name</th>
          <th class="py-2 pr-4 text-left">Kind</th>
          <th class="py-2 pr-4 text-left">Status</th>
          <th class="py-2 pr-4 text-left">Enabled</th>
          <th class="py-2 pr-4 text-left">Access</th>
          <th class="py-2 text-right">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each connections as conn (conn.id)}
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <td class="py-2 pr-4 font-medium">{conn.name}</td>
            <td class="py-2 pr-4 uppercase text-xs opacity-70">{conn.kind}</td>
            <td class="py-2 pr-4">
              <span class="flex items-center gap-2">
                <span class="status-dot" data-state={conn.status?.state || 'disabled'}
                      style="background-color: {statusColor(conn.status)};"></span>
                <span class="text-xs opacity-70">{statusLabel(conn.status)}</span>
              </span>
            </td>
            <td class="py-2 pr-4">
              <label class="switch" title={conn.enabled ? 'Disable' : 'Enable'}>
                <input type="checkbox" checked={conn.enabled} disabled={!canEdit(conn)}
                       onchange={() => toggleEnabled(conn)} />
                <span class="slider"></span>
              </label>
            </td>
            <td class="py-2 pr-4 text-xs opacity-70">{accessLabel(conn)}</td>
            <td class="py-2 text-right whitespace-nowrap">
              {#if testResults[conn.id]}
                <span class="test-result text-xs mr-2"
                      style="color: {testResults[conn.id].running
                        ? 'var(--theme-text-muted, #a6adc8)'
                        : testResults[conn.id].ok
                          ? 'var(--theme-success, #a6e3a1)'
                          : 'var(--theme-error, #f38ba8)'};">
                  {testResults[conn.id].running ? 'Testing...' : testResults[conn.id].message}
                </span>
              {/if}
              {#if canEdit(conn)}
                <button class="btn btn-secondary btn-sm mr-1" onclick={() => runTest(conn)}
                        disabled={testResults[conn.id]?.running}>
                  Test
                </button>
                <button class="icon-btn" title="Edit" onclick={() => openEdit(conn)}>
                  <i class="la la-pen"></i>
                </button>
              {/if}
              {#if conn.access === 'admin'}
                <a class="icon-btn" title="Share" aria-label="Share" href="/settings/connections/{conn.id}/share" use:link>
                  <i class="la la-share-alt"></i>
                </a>
                <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => remove(conn)}>
                  <i class="la la-trash"></i>
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

{#if showModal}
  <Modal title={editing ? 'Edit Connection' : 'New Connection'} onclose={closeModal}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); save(); }} class="flex flex-col gap-3">
      {#if formError}
        <div class="p-2 rounded text-sm"
             style="background-color: var(--theme-error); color: var(--theme-base);">
          {formError}
        </div>
      {/if}
      <FormField label="Name" id="conn-name">
        <input id="conn-name" class="input w-full" bind:value={form.name}
               placeholder="e.g. Home Broker" required />
      </FormField>
      <FormField label="Kind" id="conn-kind">
        <select
          id="conn-kind"
          class="input w-full"
          value={form.kind}
          disabled={!!editing}
          onchange={(e) => switchKind(e.currentTarget.value)}
        >
          {#each kinds as k (k.kind)}
            <option value={k.kind}>{k.label}</option>
          {/each}
        </select>
      </FormField>
      <!-- The config form renders the kind's registered schema, so a plugin's
           kind gets a full settings form with no change here. -->
      {#if kindDef}
        <ConfigForm
          spec={kindDef.config_spec}
          values={config}
          idPrefix="conn"
          onChange={setConfigField}
        />
      {/if}
      {#if editing && importStatus?.enabled}
        <div class="import-status text-sm">
          <div class="font-medium mb-1">Auto import</div>
          <div class="opacity-80">
            {importStatus.imported} device{importStatus.imported === 1 ? '' : 's'} imported{importStatus.remove_after
              ? `; devices not reported for ${importStatus.remove_after} are removed`
              : '; devices are never removed'}.
          </div>
          {#if importStatus.last_import_at || importStatus.last_removal_at}
            <div class="text-xs opacity-60 mt-1">
              {#if importStatus.last_import_at}Last import {new Date(importStatus.last_import_at).toLocaleString()}.{/if}
              {#if importStatus.last_removal_at}Last removal {new Date(importStatus.last_removal_at).toLocaleString()}.{/if}
            </div>
          {/if}
          {#if importStatus.skipped.length > 0}
            <details class="mt-2">
              <summary class="text-xs cursor-pointer">Skipped {importStatus.skipped.length}</summary>
              <ul class="text-xs mt-1 skipped-list">
                {#each importStatus.skipped as skip (skip.key)}
                  <li><span class="font-medium">{skip.name}</span>: {skip.reason}</li>
                {/each}
              </ul>
            </details>
          {/if}
        </div>
      {/if}
      {#if form.kind === 'ai' && config.provider === 'openai' && config.auth === 'chatgpt'}
        <div class="codex-login">
          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">ChatGPT account</div>
              <div class="text-xs opacity-70">Sign in with OpenAI to use your Codex subscription.</div>
            </div>
            <button
              type="button"
              class="btn btn-secondary"
              onclick={startCodexLogin}
              disabled={codexLogin === 'starting' || codexLogin === 'waiting'}
            >
              {codexLogin === 'starting'
                ? 'Starting...'
                : codexLogin === 'waiting'
                  ? 'Waiting for sign-in...'
                  : codexLogin === 'complete'
                    ? 'Sign in again'
                    : 'Sign in with ChatGPT'}
            </button>
          </div>
          <button type="button" class="mt-2 text-xs underline opacity-70" onclick={openCodexImport}>
            Import Codex auth file
          </button>
          {#if codexLoginMessage}
            <p class:error-text={codexLogin === 'error'} class="mt-2 text-xs">{codexLoginMessage}</p>
          {/if}
          {#if codexLogin === 'waiting' && codexLoginURL}
            <p class="mt-2 text-xs">
              Code: <strong>{codexUserCode}</strong>
              <a href={codexLoginURL} target="_blank" rel="noreferrer" class="ml-2 underline">Open sign-in page</a>
            </p>
          {/if}
        </div>
      {/if}
      <div class="flex gap-2 justify-end">
        <button type="button" class="btn btn-secondary" onclick={closeModal}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={saving}>
          {saving ? 'Saving...' : editing ? 'Save' : 'Create'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

{#if showCodexImport}
  <Modal title="Import Codex auth file" onclose={() => (showCodexImport = false)} zIndex={60}>
    <p class="text-sm opacity-70">
      Paste the contents of <code>~/.codex/auth.json</code>. Karakuri verifies the account after you save the connection.
    </p>
    {#if codexImportError}
      <div class="p-2 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">
        {codexImportError}
      </div>
    {/if}
    <textarea
      class="input w-full font-mono text-xs"
      rows="10"
      bind:value={codexImportValue}
      placeholder={codexImportPlaceholder}
      aria-label="Codex auth file"
    ></textarea>
    <div class="flex gap-2 justify-end">
      <button type="button" class="btn btn-secondary" onclick={() => (showCodexImport = false)}>Cancel</button>
      <button type="button" class="btn btn-primary" onclick={useImportedCodexCredentials}>Use credentials</button>
    </div>
  </Modal>
{/if}

<style>
  .status-dot {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .codex-login {
    padding: 0.75rem;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 0.375rem;
  }

  .error-text {
    color: var(--theme-error, #f38ba8);
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

  .switch {
    position: relative;
    display: inline-block;
    width: 34px;
    height: 20px;
  }

  .switch input {
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    position: absolute;
    cursor: pointer;
    inset: 0;
    background-color: var(--theme-surface2, #585b70);
    border-radius: 20px;
    transition: background-color 0.15s;
  }

  .slider::before {
    content: '';
    position: absolute;
    height: 14px;
    width: 14px;
    left: 3px;
    bottom: 3px;
    background-color: var(--theme-base, #1e1e2e);
    border-radius: 50%;
    transition: transform 0.15s;
  }

  .switch input:checked + .slider {
    background-color: var(--theme-success, #a6e3a1);
  }

  .switch input:checked + .slider::before {
    transform: translateX(14px);
  }
  .import-status {
    padding: 0.6rem 0.75rem;
    border-radius: 0.375rem;
    border: 1px solid var(--theme-surface1, #45475a);
  }

  .skipped-list {
    max-height: 10rem;
    overflow-y: auto;
  }
</style>
