<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../../lib/api/client';
  import { connectionsStore } from '../../lib/stores/connections';
  import { controlsStore } from '../../lib/stores/controls';
  import { mqttDevicesStore } from '../../lib/stores/mqttDevices';
  import { confirm } from '../../lib/stores/confirmDialog';
  import { hasPermission } from '../../lib/stores/auth';
  import {
    isWritable,
    forwardBody,
    formatBytes,
    type HistoryForward,
    type ForwardSourceType,
    type DestinationStatus,
    type ConnectionWriterStatus,
  } from '../../lib/stores/history';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import HistoryDestinations from '../../lib/components/HistoryDestinations.svelte';

  let loading = $state(true);
  let error = $state('');
  let forwards = $state<HistoryForward[]>([]);
  let statuses = $state<ConnectionWriterStatus[]>([]);

  let canWrite = $derived(hasPermission('api:connections:write'));

  let connections = $derived($connectionsStore.connections || []);
  let writable = $derived(connections.filter(isWritable));
  let haConnections = $derived(connections.filter((c) => c.kind === 'homeassistant'));
  let controls = $derived($controlsStore.controls);
  let devices = $derived($mqttDevicesStore.devices);

  const SOURCE_LABELS: Record<ForwardSourceType, string> = {
    control: 'Controls',
    mqtt_device: 'MQTT devices',
    ha: 'Home Assistant',
  };

  // The modal edits text fields for the lists: one pattern or attribute per
  // line reads better than a chip editor for entity IDs.
  interface Form {
    name: string;
    enabled: boolean;
    source_type: ForwardSourceType;
    source_ids: string[];
    connection_id: string;
    include: string;
    exclude: string;
    attributes: string;
    all_attributes: boolean;
    destinations: string[];
    metric: string;
    labels: { key: string; value: string }[];
    min_interval_s: number;
  }

  let showModal = $state(false);
  let editing: HistoryForward | null = $state(null);
  let saving = $state(false);
  let formError = $state('');
  let form = $state<Form>(emptyForm());

  let refreshTimer: ReturnType<typeof setInterval> | null = null;

  onMount(async () => {
    await refresh();
    // Controls and devices only name the sources; a user who cannot read them
    // still sees the forwards, with counts instead of names.
    await Promise.allSettled([connectionsStore.refresh(), controlsStore.load(), mqttDevicesStore.load()]);
    loading = false;
    // Writer state moves on its own as batches go out or fail.
    refreshTimer = setInterval(() => {
      if (!showModal) refresh();
    }, 10000);
  });

  onDestroy(() => {
    if (refreshTimer) clearInterval(refreshTimer);
  });

  async function refresh() {
    try {
      const [list, status] = await Promise.all([
        api.history.listForwards<HistoryForward[]>(),
        api.history.status<ConnectionWriterStatus[]>(),
      ]);
      forwards = list || [];
      statuses = status || [];
      error = '';
    } catch (e: any) {
      error = e.message || 'Failed to load history forwarding';
    } finally {
      loading = false;
    }
  }

  function emptyForm(): Form {
    return {
      name: '',
      enabled: true,
      source_type: 'control',
      source_ids: [],
      connection_id: '',
      include: '',
      exclude: '',
      attributes: '',
      all_attributes: false,
      destinations: [],
      metric: '',
      labels: [],
      min_interval_s: 0,
    };
  }

  function openCreate() {
    editing = null;
    formError = '';
    form = emptyForm();
    form.connection_id = haConnections[0]?.id ?? '';
    showModal = true;
  }

  function openEdit(f: HistoryForward) {
    editing = f;
    formError = '';
    form = {
      name: f.name,
      enabled: f.enabled,
      source_type: f.source_type,
      source_ids: [...(f.source_ids ?? [])],
      connection_id: f.connection_id ?? haConnections[0]?.id ?? '',
      include: (f.include ?? []).join('\n'),
      exclude: (f.exclude ?? []).join('\n'),
      attributes: (f.attributes ?? []).join('\n'),
      all_attributes: f.all_attributes,
      destinations: [...(f.destinations ?? [])],
      metric: f.metric ?? '',
      labels: Object.entries(f.labels ?? {}).map(([key, value]) => ({ key, value })),
      min_interval_s: (f.min_interval_ms || 0) / 1000,
    };
    showModal = true;
  }

  function lines(text: string): string[] {
    return text
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean);
  }

  function toggleSource(id: string, on: boolean) {
    form.source_ids = on
      ? [...form.source_ids.filter((s) => s !== id), id]
      : form.source_ids.filter((s) => s !== id);
  }

  function validateForm(): string {
    if (!form.name.trim()) return 'Name is required';
    if (form.source_type === 'ha') {
      if (!form.connection_id) return 'Pick a Home Assistant connection';
      if (lines(form.include).length === 0) return 'Add at least one include pattern';
    }
    if (form.destinations.length === 0) return 'Pick at least one destination';
    const seconds = Number(form.min_interval_s ?? 0);
    if (!Number.isFinite(seconds) || seconds < 0 || seconds > 86400) {
      return 'Minimum interval must be 0 to 86400 seconds';
    }
    const keys = form.labels.map((l) => l.key.trim()).filter(Boolean);
    if (new Set(keys).size !== keys.length) return 'Label names must not repeat';
    return '';
  }

  async function save() {
    formError = validateForm();
    if (formError) return;
    const labels: Record<string, string> = {};
    for (const l of form.labels) {
      if (l.key.trim()) labels[l.key.trim()] = l.value;
    }
    const ha = form.source_type === 'ha';
    const body: Record<string, unknown> = {
      name: form.name.trim(),
      enabled: form.enabled,
      source_type: form.source_type,
      source_ids: ha ? [] : form.source_ids,
      include: ha ? lines(form.include) : [],
      exclude: ha ? lines(form.exclude) : [],
      attributes: ha ? lines(form.attributes) : [],
      all_attributes: ha && form.all_attributes,
      destinations: form.destinations,
      metric: form.metric.trim(),
      labels,
      min_interval_ms: Math.round((Number(form.min_interval_s) || 0) * 1000),
    };
    if (ha) body.connection_id = form.connection_id;
    // An editor-owned forward stays with its editor; the server pins its
    // source_ids to the owner.
    if (editing?.owner_id) body.owner_id = editing.owner_id;
    saving = true;
    try {
      if (editing) await api.history.updateForward(editing.id, body);
      else await api.history.createForward(body);
      showModal = false;
      await refresh();
    } catch (e: any) {
      formError = e.message || 'Failed to save the forward';
    } finally {
      saving = false;
    }
  }

  async function setEnabled(f: HistoryForward, enabled: boolean) {
    error = '';
    try {
      await api.history.updateForward(f.id, { ...forwardBody(f), enabled });
      await refresh();
    } catch (e: any) {
      error = e.message || 'Failed to update the forward';
    }
  }

  async function remove(f: HistoryForward) {
    const ok = await confirm(
      `Delete forward "${f.name}"? Points already written stay in the databases.`,
    );
    if (!ok) return;
    error = '';
    try {
      await api.history.deleteForward(f.id);
      await refresh();
    } catch (e: any) {
      error = e.message || 'Failed to delete the forward';
    }
  }

  function sourceSummary(f: HistoryForward): string {
    const ids = f.source_ids ?? [];
    if (f.source_type === 'ha') {
      const conn = connections.find((c) => c.id === f.connection_id)?.name ?? 'Home Assistant';
      const include = (f.include ?? []).join(', ');
      const excluded = (f.exclude ?? []).length;
      return `${conn}: ${include}${excluded ? ` (${excluded} excluded)` : ''}`;
    }
    const list: { id: string; name: string }[] = f.source_type === 'control' ? controls : devices;
    const noun = f.source_type === 'control' ? 'control' : 'MQTT device';
    if (ids.length === 0) return f.source_type === 'control' ? 'All controls' : 'All MQTT devices';
    if (ids.length === 1) {
      const name = list.find((s) => s.id === ids[0])?.name;
      return name ? `${noun[0].toUpperCase()}${noun.slice(1)}: ${name}` : `1 ${noun}`;
    }
    return `${ids.length} ${noun}s`;
  }

  // destinationState reduces a destination to one badge: missing, waiting,
  // or the writer's own state, with the buffer when points are queued.
  function destinationState(d: DestinationStatus): string {
    if (d.missing) return 'missing';
    return d.writer?.state ?? 'idle';
  }

  function destinationTitle(d: DestinationStatus): string {
    if (d.missing) return 'The connection no longer exists or cannot be written.';
    const w = d.writer;
    if (!w) return 'No points handled yet.';
    const parts = [`${w.written} written`];
    if (w.buffered_points) parts.push(`${w.buffered_points} buffered`);
    if (w.last_error) parts.push(`last error: ${w.last_error}`);
    return parts.join(', ');
  }

  function formatTime(ts?: string): string {
    if (!ts) return '—';
    return new Date(ts).toLocaleString();
  }
</script>

<h2 class="text-xl font-bold mb-2">History forwarding</h2>
<p class="text-sm mb-6" style="color: var(--theme-text-muted, #a6adc8)">
  A forward records control, MQTT device, or Home Assistant values in time-series
  connections: Prometheus, InfluxDB, or PostgreSQL. Points a destination cannot
  take right away are buffered on disk and sent when it comes back. Saving a forward
  takes effect at once.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

{#if canWrite}
  <div class="flex justify-end mb-4">
    <button class="btn btn-primary" onclick={openCreate}>
      <i class="las la-plus mr-1"></i>Add Forward
    </button>
  </div>
{/if}

{#if loading}
  <p class="opacity-60">Loading forwards...</p>
{:else}
  {#if forwards.length === 0}
    <p class="opacity-60 text-center py-8">
      No forwards yet. Add one here, or tick "Record history" on a control or MQTT device.
    </p>
  {:else}
    <div class="overflow-x-auto mb-8">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <th class="py-2 pr-4 text-left">Name</th>
            <th class="py-2 pr-4 text-left">Enabled</th>
            <th class="py-2 pr-4 text-left">Source</th>
            <th class="py-2 pr-4 text-left">Destinations</th>
            <th class="py-2 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each forwards as f (f.id)}
            <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
              <td class="py-2 pr-4 font-medium">
                {f.name}
                {#if f.owner_id}
                  <div class="text-xs opacity-60">
                    from the {f.source_type === 'control' ? 'control' : 'device'} editor
                  </div>
                {/if}
              </td>
              <td class="py-2 pr-4">
                <input
                  type="checkbox"
                  checked={f.enabled}
                  disabled={!canWrite}
                  title={f.enabled ? 'Recording' : 'Switched off'}
                  onchange={(e) => setEnabled(f, e.currentTarget.checked)}
                />
              </td>
              <td class="py-2 pr-4 text-xs">{sourceSummary(f)}</td>
              <td class="py-2 pr-4 text-xs">
                <div class="flex flex-wrap gap-1">
                  {#each f.status ?? [] as d (d.connection_id)}
                    <span class="dest" data-state={destinationState(d)} title={destinationTitle(d)}>
                      {d.name || d.connection_id.slice(0, 8)}
                      <span class="opacity-80">· {destinationState(d)}</span>
                      {#if d.writer?.buffered_points}
                        <span class="opacity-80">· {d.writer.buffered_points} buffered</span>
                      {/if}
                    </span>
                  {/each}
                </div>
              </td>
              <td class="py-2 text-right whitespace-nowrap">
                {#if canWrite}
                  <button class="icon-btn" title="Edit" onclick={() => openEdit(f)}>
                    <i class="la la-pen"></i>
                  </button>
                  <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => remove(f)}>
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

  <h3 class="text-lg font-semibold mb-2">Writers</h3>
  {#if statuses.length === 0}
    <p class="opacity-60 text-sm">No writable time-series connections.</p>
  {:else}
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <th class="py-2 pr-4 text-left">Connection</th>
            <th class="py-2 pr-4 text-left">State</th>
            <th class="py-2 pr-4 text-right">Buffered</th>
            <th class="py-2 pr-4 text-right">Written</th>
            <th class="py-2 pr-4 text-right">Discarded</th>
            <th class="py-2 pr-4 text-right">Rejected</th>
            <th class="py-2 pr-4 text-left">Last success</th>
            <th class="py-2 text-left">Last error</th>
          </tr>
        </thead>
        <tbody>
          {#each statuses as s (s.connection_id)}
            {@const w = s.writer}
            <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
              <td class="py-2 pr-4 font-medium">
                {s.name} <span class="text-xs opacity-60">{s.kind}</span>
              </td>
              <td class="py-2 pr-4 text-xs">
                <span class="dest" data-state={w?.state ?? 'idle'}>{w?.state ?? 'idle'}</span>
              </td>
              <td class="py-2 pr-4 text-xs text-right">
                {w?.buffered_points ?? 0}
                <span class="opacity-60">({formatBytes(w?.buffered_bytes ?? 0)})</span>
              </td>
              <td class="py-2 pr-4 text-xs text-right">{w?.written ?? 0}</td>
              <td class="py-2 pr-4 text-xs text-right">{w?.discarded ?? 0}</td>
              <td class="py-2 pr-4 text-xs text-right">{w?.rejected ?? 0}</td>
              <td class="py-2 pr-4 text-xs">{formatTime(w?.last_success)}</td>
              <td class="py-2 text-xs">
                {#if w?.last_error}
                  <span class="error-text" title={formatTime(w.last_error_at)}>{w.last_error}</span>
                {:else}
                  —
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}

{#if showModal}
  <Modal title={editing ? 'Edit Forward' : 'New Forward'} maxWidth="2xl"
         onclose={() => (showModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); save(); }} class="flex flex-col gap-3">
      {#if formError}
        <div class="p-2 rounded text-sm"
             style="background-color: var(--theme-error); color: var(--theme-base);">
          {formError}
        </div>
      {/if}
      {#if editing?.owner_id}
        <p class="text-xs opacity-70">
          This forward is from the {editing.source_type === 'control' ? 'control' : 'device'}
          editor. Changes here show there too, and deleting the
          {editing.source_type === 'control' ? 'control' : 'device'} deletes it.
        </p>
      {/if}

      <FormField label="Name" id="forward-name">
        <input id="forward-name" class="input w-full" bind:value={form.name}
               placeholder="e.g. Temperatures to InfluxDB" />
      </FormField>

      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" bind:checked={form.enabled} />
        <span>Enabled</span>
      </label>

      <FormField label="Source" id="forward-source">
        <select id="forward-source" class="input w-full" bind:value={form.source_type}
                disabled={!!editing?.owner_id}>
          {#each Object.entries(SOURCE_LABELS) as [value, label] (value)}
            <option {value}>{label}</option>
          {/each}
        </select>
      </FormField>

      {#if form.source_type === 'control' || form.source_type === 'mqtt_device'}
        {@const list = form.source_type === 'control' ? controls : devices}
        {#if !editing?.owner_id}
          <div>
            <span class="block text-sm font-medium mb-1">
              {form.source_type === 'control' ? 'Controls' : 'MQTT devices'}
            </span>
            <div class="source-list">
              {#each list as item (item.id)}
                <label class="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={form.source_ids.includes(item.id)}
                    onchange={(e) => toggleSource(item.id, e.currentTarget.checked)}
                  />
                  <span>{item.name}</span>
                </label>
              {:else}
                <p class="text-xs opacity-60">None defined yet.</p>
              {/each}
            </div>
            <p class="text-xs mt-1 opacity-60">
              Pick none to record all of them, including ones added later. Every attribute
              is recorded.
            </p>
          </div>
        {/if}
      {:else}
        <FormField label="Home Assistant connection" id="forward-ha">
          <select id="forward-ha" class="input w-full" bind:value={form.connection_id}>
            {#each haConnections as conn (conn.id)}
              <option value={conn.id}>{conn.name}</option>
            {/each}
          </select>
          {#if haConnections.length === 0}
            <p class="text-xs mt-1 opacity-60">Add a Home Assistant connection first.</p>
          {/if}
        </FormField>
        <FormField label="Include entities" id="forward-include">
          <textarea id="forward-include" class="input w-full mono" rows="3"
                    bind:value={form.include}
                    placeholder={'sensor.*_temperature\nclimate.*'}></textarea>
          <p class="text-xs mt-1 opacity-60">
            One entity ID pattern per line. <span class="mono">*</span> matches any run of
            characters.
          </p>
        </FormField>
        <FormField label="Exclude entities" id="forward-exclude">
          <textarea id="forward-exclude" class="input w-full mono" rows="2"
                    bind:value={form.exclude}
                    placeholder="sensor.*_battery_temperature"></textarea>
          <p class="text-xs mt-1 opacity-60">
            Patterns left out, even when an include pattern matches them.
          </p>
        </FormField>
        <FormField label="Attributes" id="forward-attributes">
          <textarea id="forward-attributes" class="input w-full mono" rows="2"
                    bind:value={form.attributes}
                    placeholder="current_temperature"
                    disabled={form.all_attributes}></textarea>
          <p class="text-xs mt-1 opacity-60">
            Attributes recorded besides the state, one per line.
          </p>
        </FormField>
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" bind:checked={form.all_attributes} />
          <span>Record all numeric attributes</span>
        </label>
      {/if}

      <div>
        <span class="block text-sm font-medium mb-1">Destinations</span>
        <HistoryDestinations connections={writable} bind:selected={form.destinations}
                             idPrefix="forward-dest" />
      </div>

      <FormField label="Metric name" id="forward-metric">
        <input id="forward-metric" class="input w-full mono" bind:value={form.metric}
               placeholder={form.source_type === 'ha'
                 ? 'karakuri_ha_entity'
                 : form.source_type === 'control' ? 'karakuri_control' : 'karakuri_mqtt_device'} />
        <p class="text-xs mt-1 opacity-60">
          Optional. The metric or measurement name; empty uses the default shown.
        </p>
      </FormField>

      <div>
        <span class="block text-sm font-medium mb-1">Labels</span>
        <div class="flex flex-col gap-1">
          {#each form.labels as label, i (i)}
            <div class="flex gap-1 items-center">
              <input class="input flex-1 mono" placeholder="name" bind:value={label.key}
                     aria-label="Label name" />
              <input class="input flex-1" placeholder="value" bind:value={label.value}
                     aria-label="Label value" />
              <button type="button" class="icon-btn icon-btn-danger" title="Remove label"
                      onclick={() => (form.labels = form.labels.filter((_, j) => j !== i))}>
                <i class="la la-times"></i>
              </button>
            </div>
          {/each}
          <div>
            <button type="button" class="btn btn-secondary btn-sm"
                    onclick={() => (form.labels = [...form.labels, { key: '', value: '' }])}>
              <i class="las la-plus mr-1"></i>Add label
            </button>
          </div>
        </div>
        <p class="text-xs mt-1 opacity-60">Added to every point this forward writes.</p>
      </div>

      <FormField label="Minimum interval (seconds)" id="forward-interval">
        <input id="forward-interval" class="input w-full" type="number" min="0" max="86400"
               step="any" bind:value={form.min_interval_s} />
        <p class="text-xs mt-1 opacity-60">
          Changes closer together than this are coalesced to the latest. 0 records every change.
        </p>
      </FormField>

      <div class="flex gap-2 justify-end">
        <button type="button" class="btn btn-secondary" onclick={() => (showModal = false)}>
          Cancel
        </button>
        <button type="submit" class="btn btn-primary" disabled={saving}>
          {saving ? 'Saving...' : editing ? 'Save' : 'Create'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  .mono {
    font-family: var(--theme-font-mono, ui-monospace, monospace);
  }

  .source-list {
    max-height: 12rem;
    overflow-y: auto;
    padding: 0.5rem;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 0.375rem;
  }

  .dest {
    display: inline-block;
    padding: 0.1rem 0.45rem;
    border-radius: 4px;
    background-color: var(--theme-surface1, #45475a);
    white-space: nowrap;
  }

  .dest[data-state='ok'] {
    background-color: var(--theme-success, #a6e3a1);
    color: var(--theme-base, #1e1e2e);
  }

  .dest[data-state='failing'],
  .dest[data-state='missing'] {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }

  .dest[data-state='disabled'] {
    opacity: 0.6;
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
</style>
