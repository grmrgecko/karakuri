<script lang="ts">
  import { api } from '../../lib/api/client';
  import { mqttDevicesStore } from '../../lib/stores/mqttDevices';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';

  // Discovery lists what Zigbee2MQTT knows and adds the chosen devices as
  // ordinary MQTT devices. It only reads the bridge; nothing is published.
  let {
    connections,
    onclose,
  }: {
    connections: { id: string; name: string }[];
    onclose: () => void;
  } = $props();

  interface Discovered {
    ieee_address: string;
    friendly_name: string;
    type: string;
    power_source?: string;
    model?: string;
    vendor?: string;
    description?: string;
    kind?: string;
    commandable: boolean;
    fields?: string[];
    can_add: boolean;
    reason?: string;
    device_id?: string;
  }

  let connectionId = $state('');
  $effect(() => {
    if (!connectionId && connections.length > 0) connectionId = connections[0].id;
  });
  let baseTopic = $state('zigbee2mqtt');
  let devices = $state<Discovered[]>([]);
  let selected = $state<Record<string, boolean>>({});
  let reading = $state(false);
  let adding = $state(false);
  let error = $state('');
  let results = $state<{ added: string[]; failed: { ieee_address: string; error: string }[] } | null>(null);
  let read = $state(false);

  let chosen = $derived(devices.filter((d) => d.can_add && selected[d.ieee_address]));

  async function discover() {
    error = '';
    results = null;
    reading = true;
    try {
      const res = await api.mqttDevices.discoverZigbee2MQTT<{ base_topic: string; devices: Discovered[] }>(
        connectionId,
        baseTopic.trim(),
      );
      devices = res?.devices ?? [];
      baseTopic = res?.base_topic ?? baseTopic;
      selected = {};
      read = true;
    } catch (e: any) {
      error = e.message || 'Zigbee2MQTT could not be read';
      devices = [];
      read = false;
    } finally {
      reading = false;
    }
  }

  async function add() {
    if (chosen.length === 0) return;
    error = '';
    adding = true;
    try {
      const res = await api.mqttDevices.addZigbee2MQTT<{
        added: { name: string }[];
        failed: { ieee_address: string; error: string }[];
      }>({
        connection_id: connectionId,
        base_topic: baseTopic.trim(),
        ieee_addresses: chosen.map((d) => d.ieee_address),
      });
      await mqttDevicesStore.refresh();
      await discover();
      results = { added: (res?.added ?? []).map((d) => d.name), failed: res?.failed ?? [] };
    } catch (e: any) {
      error = e.message || 'The devices could not be added';
    } finally {
      adding = false;
    }
  }

  const kindLabels: Record<string, string> = {
    boolean: 'Toggle',
    number: 'Number',
    text: 'Text',
    light: 'Light',
    cover: 'Cover',
    lock: 'Lock',
    fan: 'Fan',
    climate: 'Thermostat',
  };
</script>

<Modal title="Add from Zigbee2MQTT" maxWidth="4xl" {onclose}>
  <div class="flex flex-col gap-4">
    <p class="text-sm opacity-70">
      Reads the device list Zigbee2MQTT keeps on its bridge topic. Added devices are ordinary MQTT
      devices: their scripts are generated from what each device reports and can be edited, and
      they follow renames in Zigbee2MQTT.
    </p>

    <div class="flex gap-3 items-end">
      <FormField label="Connection" id="z2m-conn">
        <select id="z2m-conn" class="input w-full" bind:value={connectionId}>
          {#each connections as conn (conn.id)}
            <option value={conn.id}>{conn.name}</option>
          {/each}
        </select>
      </FormField>
      <FormField label="Base topic" id="z2m-base">
        <input id="z2m-base" class="input w-full mono" bind:value={baseTopic} placeholder="zigbee2mqtt" />
      </FormField>
      <button class="btn btn-secondary mb-1" onclick={discover} disabled={reading || !connectionId}>
        {reading ? 'Reading...' : 'Read devices'}
      </button>
    </div>

    {#if error}
      <div class="p-2 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">
        {error}
      </div>
    {/if}

    {#if results}
      <div class="text-sm">
        {#if results.added.length > 0}
          <p>Added {results.added.join(', ')}.</p>
        {/if}
        {#each results.failed as f (f.ieee_address)}
          <p class="text-red-400">{f.ieee_address}: {f.error}</p>
        {/each}
      </div>
    {/if}

    {#if read && devices.length === 0}
      <p class="text-sm opacity-60">Zigbee2MQTT lists no devices besides its coordinator.</p>
    {:else if devices.length > 0}
      <div class="overflow-x-auto max-h-[50vh]">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
              <th class="py-2 pr-2 text-left"></th>
              <th class="py-2 pr-4 text-left">Device</th>
              <th class="py-2 pr-4 text-left">Model</th>
              <th class="py-2 pr-4 text-left">Added as</th>
              <th class="py-2 text-left">Also reports</th>
            </tr>
          </thead>
          <tbody>
            {#each devices as d (d.ieee_address)}
              <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)" class:opacity-60={!d.can_add}>
                <td class="py-2 pr-2">
                  <input
                    type="checkbox"
                    aria-label={`Add ${d.friendly_name}`}
                    disabled={!d.can_add}
                    checked={!!selected[d.ieee_address]}
                    onchange={(e) => (selected = { ...selected, [d.ieee_address]: e.currentTarget.checked })}
                  />
                </td>
                <td class="py-2 pr-4">
                  <div class="font-medium">{d.friendly_name}</div>
                  <div class="text-xs opacity-60 mono">{d.ieee_address}</div>
                </td>
                <td class="py-2 pr-4 text-xs">
                  {[d.vendor, d.model].filter(Boolean).join(' ') || 'unknown'}
                  {#if d.description}<div class="opacity-60">{d.description}</div>{/if}
                  {#if d.power_source}<div class="opacity-60">{d.power_source}</div>{/if}
                </td>
                <td class="py-2 pr-4 text-xs">
                  {#if d.kind}
                    {kindLabels[d.kind] ?? d.kind}{#if !d.commandable}<span class="opacity-60"> · reports only</span>{/if}
                  {/if}
                  {#if !d.can_add}<div class="opacity-80">{d.reason}</div>{/if}
                </td>
                <td class="py-2 text-xs mono opacity-70">{(d.fields ?? []).join(', ')}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <div class="flex gap-2 justify-end">
      <button type="button" class="btn btn-secondary" onclick={onclose}>Close</button>
      <button type="button" class="btn btn-primary" onclick={add} disabled={adding || chosen.length === 0}>
        {adding ? 'Adding...' : chosen.length > 0 ? `Add ${chosen.length} ${chosen.length === 1 ? 'device' : 'devices'}` : 'Add devices'}
      </button>
    </div>
  </div>
</Modal>

<style>
  .mono {
    font-family: var(--theme-font-mono, ui-monospace, monospace);
  }
</style>
