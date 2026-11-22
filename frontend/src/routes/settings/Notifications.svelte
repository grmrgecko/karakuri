<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import { connectionsStore, type Connection } from '../../lib/stores/connections';
  import { hasPermission } from '../../lib/stores/auth';
  import { confirm } from '../../lib/stores/confirmDialog';
  import type { FieldSpec } from '../../lib/stores/nodeCatalog';
  import Modal from '../../lib/components/Modal.svelte';

  interface Service {
    id: string;
    label: string;
    config_spec: FieldSpec[];
    target_format: string;
    target_example: string;
    unavailable?: string;
  }
  interface Endpoint { id: string; label: string; target: string }

  let services = $state<Service[]>([]);
  let loading = $state(true);
  let error = $state('');
  let showModal = $state(false);
  let editing = $state<Connection | null>(null);
  let saving = $state(false);
  let formError = $state('');
  let name = $state('');
  let service = $state('http');
  let enabled = $state(true);
  let credentials = $state<Record<string, string>>({});
  let endpoints = $state<Endpoint[]>([]);
  const canWrite = $derived(hasPermission('api:connections:write'));
  const connections = $derived($connectionsStore.connections.filter((c) => c.kind === 'notification'));
  const selected = $derived(services.find((s) => s.id === service));

  onMount(async () => {
    try {
      const [catalog] = await Promise.all([
        api.get<Service[]>('/notification_services'),
        connectionsStore.refresh(),
      ]);
      services = catalog;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not load notification services';
    } finally { loading = false; }
  });

  function addEndpoint() {
    endpoints = [...endpoints, { id: `endpoint-${Array.from(crypto.getRandomValues(new Uint8Array(8)), (byte) => byte.toString(16).padStart(2, '0')).join('')}`, label: '', target: '' }];
  }

  function openCreate() {
    editing = null;
    name = '';
    service = 'http';
    enabled = true;
    credentials = {};
    endpoints = [];
    addEndpoint();
    formError = '';
    showModal = true;
  }

  function openEdit(connection: Connection) {
    editing = connection;
    name = connection.name;
    service = String(connection.config.service ?? '');
    enabled = connection.enabled;
    credentials = { ...((connection.config.credentials ?? {}) as Record<string, string>) };
    endpoints = ((connection.config.endpoints ?? []) as Endpoint[]).map((e) => ({ ...e }));
    formError = '';
    showModal = true;
  }

  function changeService(value: string) {
    service = value;
    credentials = {};
    endpoints = [];
    addEndpoint();
    formError = '';
  }

  async function save() {
    saving = true;
    formError = '';
    try {
      const config = { service, credentials, endpoints };
      if (editing) await api.connections.update(editing.id, { name, enabled, config });
      else await api.connections.create({ name, enabled, kind: 'notification', config });
      await connectionsStore.refresh();
      showModal = false;
    } catch (e) {
      formError = e instanceof Error ? e.message : 'Could not save notification service';
    } finally { saving = false; }
  }

  async function remove(connection: Connection) {
    if (!(await confirm(`Delete ${connection.name} and its allowed endpoints?`))) return;
    try {
      await api.connections.remove(connection.id);
      await connectionsStore.refresh();
    } catch (e) { error = e instanceof Error ? e.message : 'Could not delete notification service'; }
  }

  function serviceLabel(id: unknown) { return services.find((s) => s.id === id)?.label ?? String(id); }
</script>

<div class="flex items-start justify-between gap-4 mb-5">
  <div>
    <h1 class="text-xl font-semibold">Notifications</h1>
    <p class="text-sm opacity-70 mt-2">Configure a service and its allowed endpoints. Flows select an endpoint by ID.</p>
  </div>
  {#if canWrite}<button class="btn btn-primary btn-sm" onclick={openCreate} disabled={loading}>Add service</button>{/if}
</div>

{#if error}<div class="alert alert-error mb-4" role="alert">{error}</div>{/if}
{#if loading}
  <p class="opacity-60">Loading notification services…</p>
{:else if connections.length === 0}
  <p class="opacity-60">No notification services configured. Add a service and at least one allowed endpoint.</p>
{:else}
  <div class="flex flex-col gap-3">
    {#each connections as connection (connection.id)}
      <div class="border rounded p-4 flex items-start justify-between gap-4" style="border-color:var(--theme-border)">
        <div>
          <h2 class="font-semibold">{connection.name}</h2>
          <p class="text-sm opacity-70">{serviceLabel(connection.config.service)} · {connection.enabled ? 'Enabled' : 'Disabled'}</p>
          <p class="text-sm mt-2">{((connection.config.endpoints ?? []) as Endpoint[]).map((e) => e.label).join(', ')}</p>
          {#if connection.status.error}<p class="text-sm text-red-400 mt-2">{connection.status.error}</p>{/if}
        </div>
        {#if canWrite}
          <div class="flex gap-2">
            <button class="btn btn-secondary btn-sm" onclick={() => openEdit(connection)}>Edit</button>
            <button class="btn btn-secondary btn-sm" onclick={() => remove(connection)}>Delete</button>
          </div>
        {/if}
      </div>
    {/each}
  </div>
{/if}

{#if showModal}
  <Modal title={editing ? 'Edit notification service' : 'Add notification service'} onclose={() => { if (!saving) showModal = false; }}>
    <form class="flex flex-col gap-4" onsubmit={(e) => { e.preventDefault(); save(); }}>
      {#if formError}<div class="alert alert-error" role="alert">{formError}</div>{/if}
      <label class="text-sm" for="notify-name">Name</label>
      <input id="notify-name" class="input w-full" bind:value={name} required />
      <label class="text-sm" for="notify-service">Service</label>
      <select id="notify-service" class="input w-full" value={service} disabled={!!editing} onchange={(e) => changeService(e.currentTarget.value)}>
        {#each services as provider (provider.id)}
          <option value={provider.id}>{provider.label}{provider.unavailable ? ' (unavailable)' : ''}</option>
        {/each}
      </select>
      {#if selected?.unavailable}
        <p class="text-sm text-red-400">{selected.unavailable}</p>
      {:else if selected}
        {#each selected.config_spec as field (field.id)}
          <div>
            <label class="block text-sm mb-1" for={`notify-${field.id}`}>{field.label}{field.required ? ' *' : ''}</label>
            {#if field.type === 'text'}
              <textarea id={`notify-${field.id}`} class="input w-full font-mono text-sm" rows="3" value={credentials[field.id] ?? ''} required={field.required} oninput={(e) => { credentials[field.id] = e.currentTarget.value; }}></textarea>
            {:else}
              <input id={`notify-${field.id}`} class="input w-full" type={field.control === 'password' ? 'password' : 'text'} value={credentials[field.id] ?? ''} required={field.required} oninput={(e) => { credentials[field.id] = e.currentTarget.value; }} />
            {/if}
            {#if field.help}<p class="text-xs opacity-60 mt-1">{field.help}</p>{/if}
          </div>
        {/each}
        <div class="flex items-center justify-between gap-3">
          <h2 class="font-semibold">Allowed endpoints</h2>
          <button type="button" class="btn btn-secondary btn-sm" onclick={addEndpoint} disabled={endpoints.length >= 100}>Add endpoint</button>
        </div>
        <p class="text-sm opacity-70">Target format: {selected.target_format}. {selected.target_example ? `Example: ${selected.target_example}` : ''}</p>
        {#if editing}<p class="text-xs opacity-60">Masked values keep saved credentials and targets. Keep endpoint IDs unchanged to preserve flow references.</p>{/if}
        {#each endpoints as endpoint, index}
          <fieldset class="border rounded p-3 flex flex-col gap-2" style="border-color:var(--theme-border)">
            <legend class="text-sm px-1">Endpoint {index + 1}</legend>
            <label class="text-xs" for={`notify-endpoint-id-${index}`}>ID</label>
            <input id={`notify-endpoint-id-${index}`} class="input w-full font-mono text-sm" bind:value={endpoint.id} pattern={'[A-Za-z0-9_-]{1,64}'} required />
            <label class="text-xs" for={`notify-endpoint-label-${index}`}>Label</label>
            <input id={`notify-endpoint-label-${index}`} class="input w-full" bind:value={endpoint.label} required maxlength="120" />
            <label class="text-xs" for={`notify-endpoint-target-${index}`}>Target</label>
            {#if selected.target_format.startsWith('JSON')}
              <textarea id={`notify-endpoint-target-${index}`} class="input w-full font-mono text-sm" rows="3" bind:value={endpoint.target} placeholder={selected.target_example} required></textarea>
            {:else}
              <input id={`notify-endpoint-target-${index}`} class="input w-full" bind:value={endpoint.target} placeholder={selected.target_example} required />
            {/if}
            <button type="button" class="btn btn-secondary btn-sm self-end" onclick={() => { endpoints = endpoints.filter((_, i) => i !== index); }}>Remove endpoint</button>
          </fieldset>
        {/each}
      {/if}
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={enabled} /> Enabled</label>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" disabled={saving} onclick={() => { showModal = false; }}>Cancel</button>
        <button class="btn btn-primary" type="submit" disabled={saving || !!selected?.unavailable || endpoints.length === 0}>{saving ? 'Saving…' : 'Save'}</button>
      </div>
    </form>
  </Modal>
{/if}
