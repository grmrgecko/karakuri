<script lang="ts">
  import { onMount } from 'svelte';
  import { Link } from 'svelte-routing';
  import { api } from '../lib/api/client';
  import { dashboards } from '../lib/stores/dashboards';
  import { confirm } from '../lib/stores/confirmDialog';
  import {
    renders,
    renderUrl,
    scheduleUrl,
    RENDER_FORMATS,
  } from '../lib/stores/renders';
  import FormField from '../lib/components/FormField.svelte';
  import { copyText } from '../lib/utils/copyText';
  import { apiKeyStore } from '../lib/stores/apiKeys';
  import type { APIKey } from '../lib/stores/apiKeys';
  import { hasPermission } from '../lib/stores/auth';
  import type { Dashboard } from '../lib/stores/dashboards';
  import type { RenderJob, RenderSchedule } from '../lib/stores/renders';

  let { id }: { id: string } = $props();

  interface CronPreview {
    valid: boolean;
    error?: string;
    description?: string;
    fields?: string[];
    next?: string[];
  }

  let dashboard = $state<Dashboard | null>(null);
  let schedules = $state<RenderSchedule[]>([]);
  let jobs = $state<RenderJob[]>([]);
  let loading = $state(true);
  let error = $state('');
  let tab = $state<'url' | 'schedules'>('url');

  // Render URL builder.
  let width = $state(1920);
  let height = $state(1080);
  let format = $state('png');
  let previewing = $state(false);
  let previewSrc = $state('');

  // Render keys open only this dashboard's render URLs. The chosen one rides
  // in every address the page copies, so a private dashboard's render works
  // in a photo frame or a wiki page that cannot sign in.
  const canReadKeys = hasPermission('api:apikeys:read');
  const canWriteKeys = hasPermission('api:apikeys:write');
  let renderKeys = $state<APIKey[]>([]);
  let selectedKeyId = $state<number | null>(null);
  let selectedKeyValue = $state('');
  let keyError = $state('');
  // The add form names the key after what will carry it, such as the
  // hallway ESP32, so the list says which key went where.
  let showAddKey = $state(false);
  let newKeyName = $state('');
  let addingKey = $state(false);

  const keyStorage = () => `karakuri.renderKey.${dashboard?.id ?? id}`;

  function rememberKey(keyId: number | null) {
    try {
      if (keyId) localStorage.setItem(keyStorage(), String(keyId));
      else localStorage.removeItem(keyStorage());
    } catch {
      // Remembering the choice is a convenience.
    }
  }

  async function loadRenderKeys() {
    if (!dashboard || !canReadKeys) return;
    try {
      renderKeys = await apiKeyStore.list(dashboard.id);
    } catch (e: any) {
      keyError = e?.message || 'Failed to load render keys';
      return;
    }
    let remembered: number | null = null;
    try {
      const raw = localStorage.getItem(keyStorage());
      remembered = raw ? Number(raw) : null;
    } catch {
      remembered = null;
    }
    if (remembered && renderKeys.some((k) => k.id === remembered)) await selectKey(remembered);
  }

  // selectKey puts a key into the copied addresses, or takes it out again
  // when the selected key is chosen a second time.
  async function selectKey(keyId: number | null) {
    keyError = '';
    if (keyId !== null && keyId === selectedKeyId) keyId = null;
    selectedKeyId = keyId;
    selectedKeyValue = '';
    rememberKey(keyId);
    if (!keyId) return;
    try {
      selectedKeyValue = await apiKeyStore.value(keyId);
    } catch (e: any) {
      selectedKeyId = null;
      rememberKey(null);
      keyError = e?.message || 'Failed to read the render key';
    }
  }

  async function addKey() {
    if (!dashboard || !newKeyName.trim()) return;
    addingKey = true;
    keyError = '';
    try {
      const created = await apiKeyStore.create({ name: newKeyName.trim(), dashboardId: dashboard.id });
      renderKeys = [created, ...renderKeys];
      selectedKeyId = created.id;
      selectedKeyValue = created.key;
      rememberKey(created.id);
      newKeyName = '';
      showAddKey = false;
    } catch (e: any) {
      keyError = e?.message || 'Failed to add a render key';
    } finally {
      addingKey = false;
    }
  }

  async function deleteKey(key: APIKey) {
    if (!(await confirm(`Delete the render key "${key.name}"? Every address carrying it stops working.`))) return;
    keyError = '';
    try {
      await apiKeyStore.remove(key.id);
      renderKeys = renderKeys.filter((k) => k.id !== key.id);
      if (selectedKeyId === key.id) {
        selectedKeyId = null;
        selectedKeyValue = '';
        rememberKey(null);
      }
    } catch (e: any) {
      keyError = e?.message || 'Failed to delete the render key';
    }
  }

  function keyDate(at: string | null): string {
    if (!at) return 'Never';
    return new Date(at).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }

  // withKey adds the chosen render key to an address as its token.
  function withKey(url: string): string {
    if (!selectedKeyValue) return url;
    return `${url}${url.includes('?') ? '&' : '?'}token=${encodeURIComponent(selectedKeyValue)}`;
  }

  let path = $derived(dashboard ? renderUrl(dashboard.id, width, height, format) : '');
  let absoluteUrl = $derived(path ? withKey(`${window.location.origin}${path}`) : '');
  let embedSnippet = $derived(
    format === 'pdf'
      ? `<a href="${absoluteUrl}">Download PDF</a>`
      : `<img src="${absoluteUrl}" alt="${dashboard?.name ?? 'Dashboard'}" width="${width}" height="${height}" />`,
  );

  // Schedule form.
  let showForm = $state(false);
  let editingId = $state<number | null>(null);
  let formName = $state('');
  let formCron = $state('0 * * * *');
  let formLocation = $state('');
  let formFormat = $state('png');
  let formWidth = $state(1920);
  let formHeight = $state(1080);
  let formEnabled = $state(true);
  let formWebhook = $state('');
  let saving = $state(false);
  let formError = $state('');
  let cronPreview = $state<CronPreview | null>(null);
  let cronTimer: ReturnType<typeof setTimeout> | null = null;

  onMount(async () => {
    try {
      dashboard = await dashboards.get(id);
    } catch (e: any) {
      error = e?.message || 'Failed to load the dashboard';
    }
    if (dashboard) {
      await Promise.all([refresh(), loadRenderKeys()]);
    }
    loading = false;
  });

  async function refresh() {
    if (!dashboard) return;
    try {
      [schedules, jobs] = await Promise.all([
        renders.listSchedules(dashboard.id),
        renders.listJobs(dashboard.id),
      ]);
    } catch (e: any) {
      error = e?.message || 'Failed to load renders';
    }
  }

  // copied names the button whose copy just succeeded, so it can show a
  // check for a moment; a failed copy says so in the page's error line.
  let copied = $state('');
  let copiedTimer: ReturnType<typeof setTimeout> | null = null;

  const COPY_REFUSED = 'The browser refused to copy; select the text and copy it by hand.';

  async function copy(key: string, text: string) {
    if (await copyText(text)) {
      if (error === COPY_REFUSED) error = '';
      copied = key;
      if (copiedTimer) clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => (copied = ''), 1500);
    } else {
      error = COPY_REFUSED;
    }
  }

  // A preview renders for real, so it is asked for rather than shown on load:
  // every reload of this page would otherwise cost a browser page.
  async function preview() {
    previewing = true;
    if (previewSrc) URL.revokeObjectURL(previewSrc);
    previewSrc = '';
    try {
      previewSrc = await api.fetchFile(path);
    } catch (e: any) {
      previewing = false;
      error = e?.message || 'Failed to render the preview';
    }
  }

  // openFile shows a render in a new tab. The tab is opened before the fetch,
  // inside the click, or a popup blocker would stop it; the session credential
  // travels in a header, so the address bar never holds the token.
  async function openFile(file: string) {
    const tab = window.open('about:blank', '_blank');
    if (tab) tab.opener = null;
    try {
      const url = await api.fetchFile(file);
      if (tab) tab.location.href = url;
    } catch (e: any) {
      tab?.close();
      error = e?.message || 'Failed to open the render';
    }
  }

  function resetForm() {
    editingId = null;
    formName = '';
    formCron = '0 * * * *';
    formLocation = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    formFormat = 'png';
    formWidth = 1920;
    formHeight = 1080;
    formEnabled = true;
    formWebhook = '';
    formError = '';
    cronPreview = null;
  }

  function openNew() {
    resetForm();
    showForm = true;
    loadCronPreview();
  }

  function openEdit(schedule: RenderSchedule) {
    editingId = schedule.id;
    formName = schedule.name || '';
    formCron = schedule.cron_expr;
    formLocation = schedule.location || '';
    formFormat = schedule.format;
    formWidth = schedule.width;
    formHeight = schedule.height;
    formEnabled = schedule.enabled;
    formWebhook = schedule.webhook_url || '';
    formError = '';
    showForm = true;
    loadCronPreview();
  }

  // The preview comes from the server's own parser, so what this reads out is
  // what the scheduler will fire on.
  async function loadCronPreview() {
    if (!formCron.trim()) {
      cronPreview = null;
      return;
    }
    try {
      cronPreview = await api.nodeTypes.cronPreview<CronPreview>(formCron, formLocation);
    } catch {
      cronPreview = null;
    }
  }

  function onCronInput() {
    if (cronTimer) clearTimeout(cronTimer);
    cronTimer = setTimeout(() => void loadCronPreview(), 250);
  }

  async function save() {
    if (!dashboard) return;
    saving = true;
    formError = '';
    const body = {
      name: formName,
      cron_expr: formCron,
      location: formLocation,
      format: formFormat,
      width: formWidth,
      height: formHeight,
      enabled: formEnabled,
      webhook_url: formWebhook,
    };
    try {
      if (editingId !== null) {
        const updated = await renders.updateSchedule(dashboard.id, editingId, body);
        schedules = schedules.map((s) => (s.id === editingId ? updated : s));
      } else {
        schedules = [await renders.createSchedule(dashboard.id, body), ...schedules];
      }
      showForm = false;
      resetForm();
    } catch (e: any) {
      formError = e?.message || 'Failed to save the schedule';
    } finally {
      saving = false;
    }
  }

  async function toggleEnabled(schedule: RenderSchedule) {
    if (!dashboard) return;
    try {
      const updated = await renders.updateSchedule(dashboard.id, schedule.id, {
        enabled: !schedule.enabled,
      });
      schedules = schedules.map((s) => (s.id === schedule.id ? updated : s));
    } catch (e: any) {
      error = e?.message || 'Failed to update the schedule';
    }
  }

  async function remove(schedule: RenderSchedule) {
    if (!dashboard) return;
    if (!(await confirm('Delete this render schedule?'))) return;
    try {
      await renders.removeSchedule(dashboard.id, schedule.id);
      schedules = schedules.filter((s) => s.id !== schedule.id);
    } catch (e: any) {
      error = e?.message || 'Failed to delete the schedule';
    }
  }

  function formatTime(value: string): string {
    const at = new Date(value);
    return isNaN(at.getTime()) ? value : at.toLocaleString();
  }
</script>

<div class="max-w-4xl mx-auto p-6">
  {#if loading}
    <div class="text-center py-12 text-text-muted">Loading...</div>
  {:else if !dashboard}
    <div class="text-center py-12 text-text-muted">Dashboard not found.</div>
  {:else}
    <div class="flex items-center gap-3 mb-6">
      <Link to="/dashboards"><i class="la la-arrow-left text-lg"></i></Link>
      <h1 class="text-2xl font-bold">Render "{dashboard.name}"</h1>
    </div>

    {#if error}
      <div class="mb-4 text-sm" style="color: var(--theme-error, #f38ba8);">{error}</div>
    {/if}

    <div class="card p-4 mb-6">
      <div class="flex items-center justify-between mb-2">
        <h2 class="font-semibold">Access keys</h2>
        {#if canWriteKeys && !showAddKey}
          <button class="btn btn-secondary btn-sm" onclick={() => { showAddKey = true; keyError = ''; }}>
            <i class="las la-plus mr-1"></i>Add key
          </button>
        {/if}
      </div>
      <p class="text-xs mb-3 text-text-muted">
        {#if dashboard.is_public}
          This dashboard is public, so its render URLs work without a key.
        {:else}
          This dashboard is private, so its render URLs need a key.
        {/if}
        A key opens this dashboard's render URLs and nothing else, for as long as you may view the
        dashboard. Give each device its own key, named for it, so you can delete one without
        breaking the others. Choose <b>Use</b> to put a key into the addresses below.
      </p>

      {#if showAddKey}
        <form class="flex flex-wrap items-end gap-2 mb-3" onsubmit={(e: SubmitEvent) => { e.preventDefault(); addKey(); }}>
          <FormField label="Name" id="render-key-name">
            <input id="render-key-name" class="input" placeholder="e.g. Hallway ESP32" bind:value={newKeyName} required />
          </FormField>
          <button type="submit" class="btn btn-primary" disabled={addingKey || !newKeyName.trim()}>
            {addingKey ? 'Adding...' : 'Add'}
          </button>
          <button type="button" class="btn btn-secondary" onclick={() => { showAddKey = false; newKeyName = ''; }}>Cancel</button>
        </form>
      {/if}

      {#if keyError}
        <p class="text-sm mb-2" style="color: var(--theme-error, #f38ba8);">{keyError}</p>
      {/if}

      {#if !canReadKeys}
        <p class="text-sm text-text-muted">You need the API key read permission to use render keys.</p>
      {:else if renderKeys.length === 0}
        <p class="text-sm text-text-muted">No keys for this dashboard yet.</p>
      {:else}
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="row-line text-left">
                <th class="py-2 pr-4">Name</th>
                <th class="py-2 pr-4">Prefix</th>
                <th class="py-2 pr-4">Created</th>
                <th class="py-2 pr-4">Last used</th>
                <th class="py-2"></th>
              </tr>
            </thead>
            <tbody>
              {#each renderKeys as key (key.id)}
                <tr class="row-line" class:key-selected={selectedKeyId === key.id}>
                  <td class="py-2 pr-4 font-medium">{key.name}</td>
                  <td class="py-2 pr-4 font-mono text-xs text-text-muted">{key.prefix}…</td>
                  <td class="py-2 pr-4 text-xs">{keyDate(key.created_at)}</td>
                  <td class="py-2 pr-4 text-xs">{keyDate(key.last_used)}</td>
                  <td class="py-2">
                    <div class="flex gap-1 justify-end">
                      <button
                        class="btn btn-sm {selectedKeyId === key.id ? 'btn-primary' : 'btn-secondary'}"
                        aria-pressed={selectedKeyId === key.id}
                        title={selectedKeyId === key.id ? 'The addresses below carry this key; click to stop' : 'Put this key into the addresses below'}
                        onclick={() => selectKey(key.id)}
                      >
                        {#if selectedKeyId === key.id}<i class="las la-check mr-1"></i>In use{:else}Use{/if}
                      </button>
                      {#if canWriteKeys}
                        <button class="icon-btn danger" title="Delete key" aria-label="Delete key {key.name}" onclick={() => deleteKey(key)}>
                          <i class="la la-trash"></i>
                        </button>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
      {#if selectedKeyId}
        <p class="text-xs mt-3 text-text-muted">
          Anyone holding a copied address can see the render, so keep it like a password.
        </p>
      {/if}
    </div>

    <div class="flex gap-1 mb-6 tab-bar">
      <button class="tab" class:tab-active={tab === 'url'} onclick={() => (tab = 'url')}>
        Render URL
      </button>
      <button class="tab" class:tab-active={tab === 'schedules'} onclick={() => (tab = 'schedules')}>
        Schedules
      </button>
    </div>

    {#if tab === 'url'}
      <div class="card p-6">
        <p class="text-sm mb-4 text-text-muted">
          The URL below renders the dashboard's deployed layout and answers with the file, so a
          wiki page, a chat message, or a photo frame can show it without running a browser. A
          public dashboard renders for anyone; anything else needs a key in the query string,
          which the access key above adds.
        </p>

        <div class="grid grid-cols-3 gap-4 mb-4">
          <FormField label="Width" id="render-width">
            <input id="render-width" type="number" class="input w-full" bind:value={width} min="100" max="7680" />
          </FormField>
          <FormField label="Height" id="render-height">
            <input id="render-height" type="number" class="input w-full" bind:value={height} min="100" max="7680" />
          </FormField>
          <FormField label="Format" id="render-format">
            <select id="render-format" class="input w-full" bind:value={format}>
              {#each RENDER_FORMATS as f}
                <option value={f}>{f.toUpperCase()}</option>
              {/each}
            </select>
          </FormField>
        </div>

        <FormField label="Render URL" id="render-url">
          <div class="flex gap-2">
            <input id="render-url" class="input w-full font-mono text-sm" value={absoluteUrl} readonly />
            <button class="btn btn-secondary" title={copied === 'url' ? 'Copied' : 'Copy'} onclick={() => copy('url', absoluteUrl)}>
              <i class="la {copied === 'url' ? 'la-check' : 'la-copy'}"></i>
            </button>
          </div>
        </FormField>

        <div class="mt-4">
          <FormField label="Embed snippet" id="render-embed">
            <div class="flex gap-2">
              <input id="render-embed" class="input w-full font-mono text-sm" value={embedSnippet} readonly />
              <button class="btn btn-secondary" title={copied === 'embed' ? 'Copied' : 'Copy'} onclick={() => copy('embed', embedSnippet)}>
                <i class="la {copied === 'embed' ? 'la-check' : 'la-copy'}"></i>
              </button>
            </div>
          </FormField>
        </div>

        <div class="flex gap-2 mt-4">
          <button class="btn btn-primary" onclick={preview} disabled={format === 'pdf'}>
            <i class="la la-image mr-1"></i>Preview
          </button>
          <button class="btn btn-secondary" onclick={() => openFile(path)}>
            <i class="la la-external-link-alt mr-1"></i>Open
          </button>
        </div>

        {#if previewing && previewSrc}
          <div class="preview mt-4">
            <img src={previewSrc} alt="{dashboard.name} render" />
          </div>
          <p class="text-xs mt-2 text-text-muted">
            Rendering takes a moment: the server opens the dashboard in a browser and photographs
            it once it has drawn.
          </p>
        {/if}
      </div>
    {:else}
      <div class="card p-6">
        <div class="flex items-center justify-between mb-4">
          <p class="text-sm text-text-muted">
            A schedule renders on its own and serves the latest result from one address that never
            changes, which is what a wall display or an e-ink panel points at.
          </p>
          <button class="btn btn-primary shrink-0 ml-4" onclick={openNew}>New schedule</button>
        </div>

        {#if showForm}
          <form
            class="schedule-form mb-6"
            onsubmit={(e: SubmitEvent) => {
              e.preventDefault();
              void save();
            }}
          >
            <h3 class="font-semibold mb-3">{editingId !== null ? 'Edit schedule' : 'New schedule'}</h3>
            <div class="flex flex-col gap-3">
              <FormField label="Name" id="sched-name">
                <input id="sched-name" class="input w-full" bind:value={formName} placeholder="Kitchen panel" />
              </FormField>

              <FormField label="Cron expression" id="sched-cron">
                <input
                  id="sched-cron"
                  class="input w-full font-mono"
                  bind:value={formCron}
                  oninput={onCronInput}
                  placeholder="0 * * * *"
                  required
                />
                {#if cronPreview?.valid}
                  <p class="text-xs mt-1 text-text-muted">{cronPreview.description}</p>
                  {#if cronPreview.next?.length}
                    <p class="text-xs mt-1 text-text-muted">
                      Next: {formatTime(cronPreview.next[0])}
                    </p>
                  {/if}
                {:else if cronPreview?.error}
                  <p class="text-xs mt-1" style="color: var(--theme-error, #f38ba8);">
                    {cronPreview.error}
                  </p>
                {/if}
              </FormField>

              <FormField label="Time zone" id="sched-location">
                <input
                  id="sched-location"
                  class="input w-full"
                  bind:value={formLocation}
                  oninput={onCronInput}
                  placeholder="America/Chicago"
                />
              </FormField>

              <div class="grid grid-cols-3 gap-3">
                <FormField label="Width" id="sched-width">
                  <input id="sched-width" type="number" class="input w-full" bind:value={formWidth} min="100" max="7680" />
                </FormField>
                <FormField label="Height" id="sched-height">
                  <input id="sched-height" type="number" class="input w-full" bind:value={formHeight} min="100" max="7680" />
                </FormField>
                <FormField label="Format" id="sched-format">
                  <select id="sched-format" class="input w-full" bind:value={formFormat}>
                    {#each RENDER_FORMATS as f}
                      <option value={f}>{f.toUpperCase()}</option>
                    {/each}
                  </select>
                </FormField>
              </div>

              <FormField label="Webhook URL" id="sched-webhook">
                <input
                  id="sched-webhook"
                  class="input w-full"
                  bind:value={formWebhook}
                  placeholder="https://example.com/hook"
                />
              </FormField>

              <label class="flex items-center gap-2 text-sm">
                <input type="checkbox" bind:checked={formEnabled} />
                Enabled
              </label>

              {#if formError}
                <p class="text-sm" style="color: var(--theme-error, #f38ba8);">{formError}</p>
              {/if}

              <div class="flex gap-2 justify-end">
                <button
                  type="button"
                  class="btn btn-secondary"
                  onclick={() => {
                    showForm = false;
                    resetForm();
                  }}
                >
                  Cancel
                </button>
                <button type="submit" class="btn btn-primary" disabled={saving}>
                  {saving ? 'Saving...' : 'Save'}
                </button>
              </div>
            </div>
          </form>
        {/if}

        {#if schedules.length === 0}
          {#if !showForm}
            <p class="text-sm text-text-muted text-center py-6">No schedules yet.</p>
          {/if}
        {:else}
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="row-line text-left">
                  <th class="py-2 pr-4">Name</th>
                  <th class="py-2 pr-4">Cron</th>
                  <th class="py-2 pr-4">Size</th>
                  <th class="py-2 pr-4">Format</th>
                  <th class="py-2 pr-4">Enabled</th>
                  <th class="py-2 pr-4">Latest</th>
                  <th class="py-2"></th>
                </tr>
              </thead>
              <tbody>
                {#each schedules as schedule (schedule.id)}
                  <tr class="row-line">
                    <td class="py-2 pr-4">{schedule.name || '—'}</td>
                    <td class="py-2 pr-4 font-mono">{schedule.cron_expr}</td>
                    <td class="py-2 pr-4">{schedule.width}x{schedule.height}</td>
                    <td class="py-2 pr-4 uppercase">{schedule.format}</td>
                    <td class="py-2 pr-4">
                      <button
                        class="toggle"
                        onclick={() => toggleEnabled(schedule)}
                        title={schedule.enabled ? 'Click to disable' : 'Click to enable'}
                      >
                        {schedule.enabled ? 'On' : 'Off'}
                      </button>
                    </td>
                    <td class="py-2 pr-4">
                      <button
                        class="link"
                        onclick={() => openFile(scheduleUrl(dashboard!.id, schedule.id, schedule.format))}
                      >
                        Open
                      </button>
                    </td>
                    <td class="py-2">
                      <div class="flex gap-1 justify-end">
                        <button
                          class="icon-btn"
                          title={copied === `schedule-${schedule.id}` ? 'Copied' : 'Copy the latest-render URL'}
                          onclick={() =>
                            copy(
                              `schedule-${schedule.id}`,
                              withKey(`${window.location.origin}${scheduleUrl(dashboard!.id, schedule.id, schedule.format)}`),
                            )}
                        >
                          <i class="la {copied === `schedule-${schedule.id}` ? 'la-check' : 'la-copy'}"></i>
                        </button>
                        <button class="icon-btn" title="Edit" onclick={() => openEdit(schedule)}>
                          <i class="la la-pencil-alt"></i>
                        </button>
                        <button class="icon-btn danger" title="Delete" onclick={() => remove(schedule)}>
                          <i class="la la-trash"></i>
                        </button>
                      </div>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>

      <!-- Recent renders. A schedule that fires with nobody watching is only
           visible here, which is also where a failed one says why. -->
      <div class="card p-6 mt-6">
        <div class="flex items-center justify-between mb-4">
          <h3 class="font-semibold">Recent renders</h3>
          <button class="btn btn-secondary" onclick={refresh}>
            <i class="la la-sync mr-1"></i>Refresh
          </button>
        </div>
        {#if jobs.length === 0}
          <p class="text-sm text-text-muted text-center py-6">Nothing rendered yet.</p>
        {:else}
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="row-line text-left">
                  <th class="py-2 pr-4">When</th>
                  <th class="py-2 pr-4">Size</th>
                  <th class="py-2 pr-4">Format</th>
                  <th class="py-2 pr-4">Source</th>
                  <th class="py-2">Status</th>
                </tr>
              </thead>
              <tbody>
                {#each jobs.slice(0, 20) as job (job.id)}
                  <tr class="row-line">
                    <td class="py-2 pr-4">{formatTime(job.created_at)}</td>
                    <td class="py-2 pr-4">{job.width}x{job.height}</td>
                    <td class="py-2 pr-4 uppercase">{job.format}</td>
                    <td class="py-2 pr-4">{job.schedule_id ? 'Schedule' : 'On demand'}</td>
                    <td class="py-2">
                      <span class:failed={job.status === 'failed'}>{job.status}</span>
                      {#if job.error_message}
                        <span class="text-xs failed">: {job.error_message}</span>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>
    {/if}
  {/if}
</div>

<style>
  .tab-bar {
    border-bottom: 1px solid var(--theme-surface1, #45475a);
  }

  .tab {
    padding: 0.5rem 1rem;
    font-size: 0.875rem;
    font-weight: 500;
    color: var(--theme-subtext0, #a6adc8);
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    cursor: pointer;
  }

  .tab:hover {
    color: var(--theme-text, #cdd6f4);
  }

  .tab-active {
    color: var(--theme-accent, #89b4fa);
    border-bottom-color: var(--theme-accent, #89b4fa);
  }

  .key-selected {
    background: color-mix(in srgb, var(--theme-accent, #89b4fa) 10%, transparent);
  }

  .schedule-form {
    padding: 1rem;
    border-radius: 8px;
    background-color: var(--theme-surface0, #313244);
  }

  .row-line {
    border-bottom: 1px solid var(--theme-surface1, #45475a);
  }

  .link {
    color: var(--theme-accent, #89b4fa);
  }

  .link:hover {
    text-decoration: underline;
  }

  .toggle {
    background: none;
    border: none;
    cursor: pointer;
    color: var(--theme-green, #a6e3a1);
    padding: 0;
  }

  .failed {
    color: var(--theme-error, #f38ba8);
  }

  .icon-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    border: none;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
  }

  .icon-btn:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .icon-btn.danger:hover {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }

  .preview img {
    display: block;
    width: 100%;
    height: auto;
    border-radius: 8px;
    border: 1px solid var(--theme-surface1, #45475a);
  }
</style>
