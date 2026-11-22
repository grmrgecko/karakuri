<script lang="ts">
  import { api } from '../../lib/api/client';
  import { mqttDevicesStore } from '../../lib/stores/mqttDevices';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';

  // Lists a connection's entities: an ESPHome node's entities, a Frigate
  // server's camera switches, a Home Assistant's entities by area, an
  // openHAB's items by location, or an MQTT broker's discovery configs by
  // device. It adds the chosen ones as devices. Every ESPHome and Frigate
  // entity that can be added starts ticked; the others list hundreds, so none
  // are.
  let {
    kind,
    connections,
    onclose,
  }: {
    kind: 'esphome' | 'frigate' | 'homeassistant' | 'openhab' | 'mqtt';
    connections: { id: string; name: string }[];
    onclose: () => void;
  } = $props();

  const texts = {
    esphome: {
      title: 'Add from ESPHome',
      intro: 'Lists the entities of a connected ESPHome node. Each one added becomes a device, drawn with its matching control and commanded through the node.',
      none: 'Add an ESPHome connection in Connections first.',
      picker: 'Node',
      unread: 'The node could not be read',
    },
    homeassistant: {
      title: 'Add from Home Assistant',
      intro: 'Lists the entities of a connected Home Assistant by floor and area. Each one added becomes a device drawn as a card would draw it; flows read it and command it by kind, and commands are Home Assistant service calls.',
      none: 'Add a Home Assistant connection in Connections first.',
      picker: 'Connection',
      unread: 'Home Assistant could not be read',
    },
    openhab: {
      title: 'Add from openHAB',
      intro: 'Lists the items of a connected openHAB by floor and location. Each one added becomes a device drawn with the control its item type maps to; commands are openHAB commands, held to the item\'s limits before they are sent.',
      none: 'Add an openHAB connection in Connections first.',
      picker: 'Connection',
      unread: 'openHAB could not be read',
    },
    mqtt: {
      title: 'Add from MQTT discovery',
      intro: 'Lists the devices announcing themselves with Home Assistant MQTT discovery on a connection, such as Tasmota, Shelly, and ESPHome nodes flashed for MQTT. Each one added becomes a device with its scripts written from its discovery config.',
      none: 'Add an MQTT connection in Connections first.',
      picker: 'Connection',
      unread: 'The discovery configs could not be read',
    },
    frigate: {
      title: 'Add from Frigate',
      intro: "Lists each camera's detect, record, snapshots, and motion switches on a connected Frigate server. Each one added becomes a toggle device that switches the feature in Frigate and is unavailable while its camera receives no frames.",
      none: 'Add a Frigate connection in Connections first.',
      picker: 'Server',
      unread: 'The server could not be read',
    },
  };
  let text = $derived(texts[kind]);
  // A Home Assistant or openHAB lists a house's worth of entities by area, so
  // the list is grouped and filterable and nothing starts ticked.
  let grouped = $derived(kind === 'homeassistant' || kind === 'openhab' || kind === 'mqtt');

  interface Discovered {
    object_id: string;
    name: string;
    type: string;
    kind?: string;
    commandable: boolean;
    can_add: boolean;
    reason?: string;
    device_id?: string;
    group?: string;
  }

  let connectionId = $state('');
  $effect(() => {
    if (!connectionId && connections.length > 0) connectionId = connections[0].id;
  });
  let node = $state<{ name: string; board?: string; version?: string } | null>(null);
  let entities = $state<Discovered[]>([]);
  let selected = $state<Record<string, boolean>>({});
  let reading = $state(false);
  let adding = $state(false);
  let error = $state('');
  let results = $state<{ added: string[]; failed: { object_id: string; error: string }[] } | null>(null);

  let chosen = $derived(entities.filter((e) => e.can_add && selected[e.object_id]));

  async function read() {
    error = '';
    reading = true;
    try {
      const res =
        kind === 'esphome'
          ? await api.mqttDevices.esphomeEntities<{
              node: { name: string; board?: string; version?: string };
              entities: Discovered[];
            }>(connectionId)
          : kind === 'mqtt'
            ? await api.mqttDevices
                .mqttDiscovery<{ devices: Discovered[] }>(connectionId)
                .then((r) => ({ entities: r?.devices ?? [] }))
            : await api.mqttDevices.entities<{ entities: Discovered[] }>(connectionId);
      node = (res as any)?.node ?? null;
      entities = res?.entities ?? [];
      const next: Record<string, boolean> = {};
      if (!grouped) for (const e of entities) if (e.can_add) next[e.object_id] = true;
      selected = next;
    } catch (e: any) {
      error = e.message || text.unread;
      entities = [];
      node = null;
    } finally {
      reading = false;
    }
  }

  async function add() {
    if (chosen.length === 0) return;
    error = '';
    adding = true;
    try {
      const body = { connection_id: connectionId, object_ids: chosen.map((e) => e.object_id) };
      type AddResult = { added: { name: string }[]; failed: { object_id: string; error: string }[] };
      const res =
        kind === 'mqtt'
          ? await api.mqttDevices.addMQTTDiscovery<AddResult>(body)
          : await api.mqttDevices.addEntities<AddResult>(body);
      await mqttDevicesStore.refresh();
      await read();
      results = { added: (res?.added ?? []).map((d) => d.name), failed: res?.failed ?? [] };
    } catch (e: any) {
      error = e.message || 'The entities could not be added';
    } finally {
      adding = false;
    }
  }

  const kindLabels: Record<string, string> = {
    boolean: 'Toggle',
    number: 'Number',
    text: 'Text',
    select: 'Dropdown',
    buttons: 'Buttons',
    light: 'Light',
    cover: 'Cover',
    lock: 'Lock',
    fan: 'Fan',
    climate: 'Thermostat',
    location: 'Location',
    media: 'Media player',
  };

  // filter narrows a long list, such as a Home Assistant's, by name, ID, or
  // area.
  let filter = $state('');
  let shown = $derived.by(() => {
    const term = filter.trim().toLowerCase();
    if (!term) return entities;
    return entities.filter((e) =>
      [e.name, e.object_id, e.group].some((v) => (v ?? '').toLowerCase().includes(term)),
    );
  });
</script>

<Modal title={text.title} maxWidth="4xl" {onclose}>
  <div class="flex flex-col gap-4">
    <p class="text-sm opacity-70">{text.intro}</p>

    {#if connections.length === 0}
      <p class="text-sm opacity-60">{text.none}</p>
    {:else}
      <div class="flex gap-3 items-end">
        <FormField label={text.picker} id="entity-conn">
          <select id="entity-conn" class="input w-full" bind:value={connectionId}>
            {#each connections as conn (conn.id)}
              <option value={conn.id}>{conn.name}</option>
            {/each}
          </select>
        </FormField>
        <button class="btn btn-secondary mb-1" onclick={read} disabled={reading || !connectionId}>
          {reading ? 'Reading...' : 'Read entities'}
        </button>
      </div>
    {/if}

    {#if error}
      <div class="p-2 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">
        {error}
      </div>
    {/if}

    {#if results}
      <div class="text-sm">
        {#if results.added.length > 0}<p>Added {results.added.join(', ')}.</p>{/if}
        {#each results.failed as f (f.object_id)}
          <p class="text-red-400">{f.object_id}: {f.error}</p>
        {/each}
      </div>
    {/if}

    {#if node}
      <p class="text-sm">
        <span class="font-medium">{node.name}</span>
        <span class="opacity-60">{[node.board, node.version ? `ESPHome ${node.version}` : ''].filter(Boolean).join(' · ')}</span>
      </p>
    {/if}

    {#if entities.length > 0}
      {#if grouped}
        <input class="input w-full" placeholder="Filter by name, ID, or area" bind:value={filter} />
      {/if}
      <div class="overflow-x-auto max-h-[50vh]">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
              <th class="py-2 pr-2 text-left"></th>
              <th class="py-2 pr-4 text-left">Entity</th>
              <th class="py-2 pr-4 text-left">Type</th>
              <th class="py-2 text-left">Added as</th>
            </tr>
          </thead>
          <tbody>
            {#each shown as e, i (e.object_id)}
              {#if grouped && (i === 0 || shown[i - 1].group !== e.group)}
                <tr><td colspan="4" class="pt-3 pb-1 text-xs uppercase opacity-60">{e.group || (kind === 'mqtt' ? 'No device' : 'No area')}</td></tr>
              {/if}
              <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)" class:opacity-60={!e.can_add}>
                <td class="py-2 pr-2">
                  <input
                    type="checkbox"
                    aria-label={`Add ${e.name}`}
                    disabled={!e.can_add}
                    checked={!!selected[e.object_id]}
                    onchange={(ev) => (selected = { ...selected, [e.object_id]: ev.currentTarget.checked })}
                  />
                </td>
                <td class="py-2 pr-4">
                  <div class="font-medium">{e.name}</div>
                  <div class="text-xs opacity-60 mono">{e.object_id}</div>
                </td>
                <td class="py-2 pr-4 text-xs">{e.type.replaceAll('_', ' ')}</td>
                <td class="py-2 text-xs">
                  {#if e.kind && e.can_add}
                    {kindLabels[e.kind] ?? e.kind}{#if !e.commandable}<span class="opacity-60"> · reports only</span>{/if}
                  {:else if e.reason}
                    <span class="opacity-80">{e.reason}</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <div class="flex gap-2 justify-end">
      <button type="button" class="btn btn-secondary" onclick={onclose}>Close</button>
      <button type="button" class="btn btn-primary" onclick={add} disabled={adding || chosen.length === 0}>
        {adding ? 'Adding...' : chosen.length > 0 ? `Add ${chosen.length} ${chosen.length === 1 ? 'entity' : 'entities'}` : 'Add entities'}
      </button>
    </div>
  </div>
</Modal>

<style>
  .mono {
    font-family: var(--theme-font-mono, ui-monospace, monospace);
  }
</style>
