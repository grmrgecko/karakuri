<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { mqttDevicesStore, type MQTTDevice } from '../../lib/stores/mqttDevices';
  import { websocket } from '../../lib/stores/websocket';
  import { connectionsStore } from '../../lib/stores/connections';
  import { controlsStore } from '../../lib/stores/controls';
  import { confirm } from '../../lib/stores/confirmDialog';
  import { hasPermission } from '../../lib/stores/auth';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import DocumentField from '../../lib/components/editor/DocumentField.svelte';
  import ConfigForm from '../../lib/components/editor/ConfigForm.svelte';
  import HistoryRecordField from '../../lib/components/HistoryRecordField.svelte';
  import Zigbee2MQTTDiscovery from './Zigbee2MQTTDiscovery.svelte';
  import EntityDiscovery from './EntityDiscovery.svelte';
  import { api } from '../../lib/api/client';
  import type { ScriptTest } from '../../lib/components/editor/scriptTest';

  let loading = $state(true);
  let error = $state('');
  let devices = $derived($mqttDevicesStore.devices);
  let mqttConnections = $derived(
    ($connectionsStore.connections || []).filter((c: any) => c.kind === 'mqtt'),
  );
  let esphomeConnections = $derived(
    ($connectionsStore.connections || []).filter((c: any) => c.kind === 'esphome'),
  );
  let frigateConnections = $derived(
    ($connectionsStore.connections || []).filter((c: any) => c.kind === 'frigate'),
  );
  let haConnections = $derived(
    ($connectionsStore.connections || []).filter((c: any) => c.kind === 'homeassistant'),
  );
  let openhabConnections = $derived(
    ($connectionsStore.connections || []).filter((c: any) => c.kind === 'openhab'),
  );
  let allConnections = $derived([
    ...mqttConnections,
    ...esphomeConnections,
    ...frigateConnections,
    ...haConnections,
    ...openhabConnections,
  ]);
  type EntitySourceKind = 'esphome' | 'frigate' | 'homeassistant' | 'openhab' | 'mqtt';
  const entityConnections = (kind: EntitySourceKind) =>
    ({
      esphome: manual(esphomeConnections),
      frigate: manual(frigateConnections),
      homeassistant: manual(haConnections),
      openhab: manual(openhabConnections),
      mqtt: manual(mqttConnections),
    })[kind];

  let canWrite = $derived(hasPermission('api:connections:write'));

  // A connection with auto import on adds its devices itself, so the pickers
  // offer only the others.
  const autoImports = (c: any) => c?.config?.auto_import === true;
  const manual = (list: any[]) => list.filter((c) => !autoImports(c));
  let autoConnectionIds = $derived(new Set(allConnections.filter(autoImports).map((c: any) => c.id)));
  function pickerTitle(list: any[], label: string): string {
    if (list.length === 0) return `Add ${label} connection in Connections first.`;
    if (manual(list).length === 0) return `Every ${label.replace(/^an? /, '')} connection imports its devices automatically.`;
    return '';
  }

  // QOS_LEVELS are MQTT's three delivery guarantees, as the selects offer them.
  const QOS_LEVELS = [
    { value: 0, label: '0: at most once' },
    { value: 1, label: '1: at least once' },
    { value: 2, label: '2: exactly once' },
  ];
  let historyField: ReturnType<typeof HistoryRecordField> | undefined = $state();

  let showModal = $state(false);
  let showDiscovery = $state(false);
  let showEntities = $state<EntitySourceKind | null>(null);
  let editing = $state<MQTTDevice | null>(null);
  // isEntity says the dialog is editing an entity device, an ESPHome entity,
  // a Frigate switch, or a Home Assistant entity, which has no topics or
  // scripts: the connection reads and commands it.
  const transportLabels: Record<string, string> = {
    esphome: 'ESPHome',
    frigate: 'Frigate',
    homeassistant: 'Home Assistant',
    openhab: 'openHAB',
  };
  let isEntity = $derived(!!editing?.transport && editing.transport in transportLabels);
  let saving = $state(false);
  let formError = $state('');
  let form = $state({
    name: '',
    description: '',
    connection_id: '',
    topics: '',
    state_script: '',
    control: 'text',
    payload_script: '',
    availability_topic: '',
    availability_online: 'online',
    availability_offline: 'offline',
    expect_every_s: 0,
    offline_commands: false,
    state_qos: 0,
    command_qos: 0,
    command_retain: false,
    state_timeout_ms: 0,
    payload_timeout_ms: 0,
  });
  // The kind's own settings, edited with the same schema-driven form the
  // controls panel uses. A device and a control answer the same question, so
  // they ask it the same way.
  let options = $state<Record<string, unknown>>({});

  let kinds = $derived($controlsStore.kinds);
  let kindDef = $derived(kinds.find((k) => k.name === form.control));

  // stateKeys are the values this kind's state script is expected to return,
  // dropping the ones whose feature is switched off. It is what makes a device
  // and a widget agree without the dashboard row being told anything.
  let stateKeys = $derived(
    (kindDef?.attributes ?? []).filter(
      (a) => !a.requires_option || options[a.requires_option] === true,
    ),
  );

  // defaultOptions seeds a kind's settings from its declared defaults.
  function defaultOptions(kind: string): Record<string, unknown> {
    const def = kinds.find((k) => k.name === kind);
    const out: Record<string, unknown> = {};
    for (const field of def?.options ?? []) {
      if (field.default !== undefined) out[field.id] = field.default;
    }
    return out;
  }

  function setOption(fieldId: string, value: unknown) {
    if (value === undefined) delete options[fieldId];
    else options[fieldId] = value;
    options = { ...options };
  }

  // An example per kind, because what a payload script reads depends on what
  // the face offers: a switch has a direction, a slider has a number, a light
  // has several attributes and says which one moved. Starting from the right
  // shape is most of writing one.
  const PAYLOAD_EXAMPLES: Record<string, string> = {
    boolean: [
      '// on is the direction being commanded, true when switching on.',
      'return {',
      '  topic: "home/device/set",',
      '  payload: { state: on ? "ON" : "OFF" },',
      '};',
    ].join('\n'),
    buttons: [
      "// value is the pressed button's own value.",
      'return { topic: "home/device/set", payload: value };',
    ].join('\n'),
    number: [
      '// value is the number the slider was moved to.',
      'return {',
      '  topic: "home/device/set",',
      '  payload: { brightness: Math.round(Number(value)) },',
      '};',
    ].join('\n'),
    select: [
      '// value is the option that was picked.',
      '// Map it to whatever the device calls it:',
      'const modes = { Cool: 3, Heat: 2, Off: 0 };',
      'return { topic: "home/device/set", payload: { mode: modes[value] ?? 0 } };',
    ].join('\n'),
    text: [
      '// value is the text that was entered.',
      'return { topic: "home/device/set", payload: { text: String(value) } };',
    ].join('\n'),
    datetime: [
      '// value is the timestamp that was picked, as text.',
      'return { topic: "home/device/set", payload: { at: String(value) } };',
    ].join('\n'),
    lock: [
      '// value is the state being asked for: locked or unlocked.',
      'return {',
      '  topic: "home/lock/set",',
      '  payload: { state: value === "locked" ? "LOCK" : "UNLOCK" },',
      '};',
    ].join('\n'),
    light: [
      '// field says which attribute moved: on, brightness, color,',
      '// color_temp, or effect. Only one moves per press.',
      'if (field === "brightness") {',
      '  // Brightness arrives as a percentage; zigbee2mqtt counts to 254.',
      '  return {',
      '    topic: "zigbee2mqtt/lamp/set",',
      '    payload: { brightness: Math.round((Number(value) / 100) * 254) },',
      '  };',
      '}',
      'if (field === "color") {',
      '  return { topic: "zigbee2mqtt/lamp/set", payload: { color: { hex: value } } };',
      '}',
      'if (field === "color_temp") {',
      '  return { topic: "zigbee2mqtt/lamp/set", payload: { color_temp_kelvin: Number(value) } };',
      '}',
      'return { topic: "zigbee2mqtt/lamp/set", payload: { state: on ? "ON" : "OFF" } };',
    ].join('\n'),
    cover: [
      '// field is "state" for open, stop, and close, or "position".',
      'if (field === "position") {',
      '  return { topic: "home/blind/set", payload: { position: Number(value) } };',
      '}',
      'const commands = { open: "OPEN", closed: "CLOSE", stopped: "STOP" };',
      'return { topic: "home/blind/set", payload: commands[value] ?? "STOP" };',
    ].join('\n'),
    climate: [
      '// field is "mode", "target", or "fan_mode".',
      'if (field === "target") {',
      '  return { topic: "home/thermostat/set", payload: { setpoint: Number(value) } };',
      '}',
      'return { topic: "home/thermostat/set", payload: { [field]: value } };',
    ].join('\n'),
    fan: [
      '// field is "on", "percentage", "preset", "oscillating", or "direction".',
      'if (field === "percentage") {',
      '  return { topic: "home/fan/set", payload: { speed: Number(value) } };',
      '}',
      'return { topic: "home/fan/set", payload: { [field]: value } };',
    ].join('\n'),
  };

  // A device that reports and never acts has nothing to send, which is what an
  // empty payload script means.
  const NO_COMMAND = [
    '// This device only reports, so there is nothing to send.',
    '// Write a script here and its rows become pressable.',
    'return null;',
  ].join('\n');

  // Swapping the kind rewrites the example and reseeds the settings, but the
  // script only while it still is an example: an author who has written
  // something keeps it.
  function controlChanged(next: string) {
    const untouched =
      form.payload_script.trim() === '' ||
      form.payload_script === NO_COMMAND ||
      Object.values(PAYLOAD_EXAMPLES).some((ex) => ex === form.payload_script);
    form.control = next;
    options = defaultOptions(next);
    if (untouched) form.payload_script = PAYLOAD_EXAMPLES[next] ?? NO_COMMAND;
  }

  const STARTER_STATE = `// topic is where this message arrived, payload is its body.
// Return only the values this message carries. Anything you leave
// out keeps the value the device already had.
// The keys the control kind reads are listed under this editor.
const d = JSON.parse(payload);
return { on: d.state === "ON" };`;

  // Both editors test themselves against a sample. The runner is the same code
  // the device runs, so what an author sees here is what will happen, and the
  // seeded document doubles as the record of what each script receives.
  const stateTest: ScriptTest = {
    title: 'Test the state script',
    hint: 'The sample is one message. Nothing is subscribed and no state changes.',
    sample: JSON.stringify(
      { topic: 'zigbee2mqtt/lamp', payload: '{"state":"ON","brightness":128}', retained: false },
      null,
      2,
    ),
    run: (script, input) => {
      const sample = (input ?? {}) as Record<string, unknown>;
      return mqttDevicesStore.test({
        mode: 'state',
        script,
        topic: String(sample.topic ?? ''),
        payload: String(sample.payload ?? ''),
        // A sample may carry the state the device is already holding, for a
        // script that reads meta.state; otherwise the device's own is used.
        state: (sample.state as Record<string, unknown>) ?? editing?.state ?? {},
        // retained true tests the message as one the broker held for a new
        // subscription; the limit is the one the form sets.
        retained: sample.retained === true,
        timeout_ms: Number(form.state_timeout_ms) || 0,
      });
    },
  };

  // firstChoice reads the first declared value of a choices option, whichever
  // of the two stored shapes it is in.
  function firstChoice(key: string): string | undefined {
    const raw = options[key];
    if (!Array.isArray(raw) || raw.length === 0) return undefined;
    const item = raw[0] as Record<string, unknown> | string;
    return typeof item === 'object' && item !== null ? String(item.value ?? '') : String(item);
  }

  // sampleCommand seeds the payload test with an interaction the current kind
  // accepts. The test holds the sample to the kind the way a real press is
  // held, so a seed the kind would refuse teaches the bounds by error instead
  // of showing the script run.
  function sampleCommand(): Record<string, unknown> {
    switch (form.control) {
      case 'boolean':
      case 'light':
      case 'fan':
        return { field: 'on', value: true, on: true, previous: false };
      case 'number': {
        const min = Number(options.min);
        return { field: 'value', value: Number.isFinite(min) ? min : 50 };
      }
      case 'select':
        return { field: 'value', value: firstChoice('options') ?? '' };
      case 'buttons':
        return { field: 'value', value: firstChoice('buttons') ?? 'pressed' };
      case 'lock':
        return { field: 'state', value: 'locked' };
      case 'cover':
        return { field: 'state', value: 'open' };
      case 'climate':
        return { field: 'mode', value: firstChoice('modes') ?? 'off' };
      case 'datetime': {
        const mode = String(options.mode ?? 'datetime');
        const value =
          mode === 'date' ? '2026-01-01' : mode === 'time' ? '08:00' : '2026-01-01T08:00:00Z';
        return { field: 'value', value };
      }
      default:
        return { field: 'value', value: 'hello', previous: '' };
    }
  }

  let payloadTest: ScriptTest = $derived({
    title: 'Test the payload script',
    hint: 'The sample is one control change. Nothing is published.',
    sample: JSON.stringify(sampleCommand(), null, 2),
    run: (script, input) => {
      const sample = (input ?? {}) as Record<string, unknown>;
      return mqttDevicesStore.test({
        mode: 'payload',
        script,
        field: String(sample.field ?? ''),
        value: sample.value,
        on: typeof sample.on === 'boolean' ? sample.on : undefined,
        previous: sample.previous,
        // The form's kind and settings ride along so the test holds the value
        // the way a real press is held: a slider outside its bounds fails
        // here, not on the wall panel.
        control: form.control,
        options: { ...options },
        timeout_ms: Number(form.payload_timeout_ms) || 0,
      });
    },
  });

  let unsubscribeWs: (() => void) | null = null;
  let clockTimer: ReturnType<typeof setInterval> | null = null;
  // now drives the "heard ... ago" labels, ticking so a device that goes
  // quiet visibly ages rather than reading "just now" forever.
  let now = $state(Date.now());

  onMount(async () => {
    try {
      await Promise.all([
        mqttDevicesStore.load(),
        connectionsStore.load(),
        // The kind catalog is what the picker and the settings form are built
        // from. It carries no control, so an author who may declare devices but
        // not controls still gets it.
        controlsStore.load(),
      ]);
    } catch (e: any) {
      error = e.message || 'Failed to load MQTT devices';
    } finally {
      loading = false;
    }

    // Decoded values move whenever a message arrives, so the list follows the
    // broker: an author watching this page sees the state script working, or
    // failing, without refreshing anything.
    unsubscribeWs = websocket.subscribeTopic('mqttdevice:*', (data) => {
      const msg = data as {
        device_id?: string;
        state?: Record<string, unknown>;
        error?: string;
        availability?: string;
        source?: string;
        at?: string;
        availability_at?: string;
      };
      if (!msg?.device_id) return;
      if (msg.error !== undefined) mqttDevicesStore.applyError(msg.device_id, String(msg.error));
      else if (msg.state) mqttDevicesStore.applyState(msg.device_id, msg.state);
      if (msg.availability) {
        mqttDevicesStore.applyAvailability(msg.device_id, msg.availability, msg.source, msg.at ?? msg.availability_at);
      }
    });
    clockTimer = setInterval(() => (now = Date.now()), 10000);
  });

  onDestroy(() => {
    unsubscribeWs?.();
    if (clockTimer) clearInterval(clockTimer);
  });

  function openCreate() {
    editing = null;
    formError = '';
    form = {
      name: '',
      description: '',
      connection_id: mqttConnections[0]?.id ?? '',
      topics: '',
      state_script: STARTER_STATE,
      control: 'boolean',
      payload_script: PAYLOAD_EXAMPLES.boolean,
      availability_topic: '',
      availability_online: 'online',
      availability_offline: 'offline',
      expect_every_s: 0,
      offline_commands: false,
      state_qos: 0,
      command_qos: 0,
      command_retain: false,
      state_timeout_ms: 0,
      payload_timeout_ms: 0,
    };
    options = defaultOptions('boolean');
    showModal = true;
  }

  function openEdit(device: MQTTDevice) {
    editing = device;
    formError = '';
    form = {
      name: device.name,
      description: device.description,
      connection_id: device.connection_id,
      // Topics are one per line: a device's topic list is short and reads
      // better stacked than comma-separated.
      topics: device.topics.join('\n'),
      state_script: device.state_script,
      control: device.control || 'text',
      payload_script: device.payload_script || '',
      availability_topic: device.availability_topic || '',
      availability_online: device.availability_online || 'online',
      availability_offline: device.availability_offline || 'offline',
      expect_every_s: device.expect_every_s || 0,
      offline_commands: device.offline_commands === true,
      state_qos: device.state_qos || 0,
      command_qos: device.command_qos || 0,
      command_retain: device.command_retain === true,
      state_timeout_ms: device.state_timeout_ms || 0,
      payload_timeout_ms: device.payload_timeout_ms || 0,
    };
    options = { ...(device.options || {}) };
    showModal = true;
  }

  function topicList(): string[] {
    return form.topics
      .split('\n')
      .map((t) => t.trim())
      .filter(Boolean);
  }

  function validateForm(): string {
    if (!form.name.trim()) return 'Name is required';
    if (!form.connection_id) return 'Pick a connection';
    if (!isEntity && topicList().length === 0) return 'A device needs at least one topic';
    if (!isEntity && !form.state_script.trim()) return 'A device needs a state script';
    const every = Number(form.expect_every_s) || 0;
    if (every < 0 || (every > 0 && every < 60)) return 'Expect a message every: 0 for off, or at least 60 seconds';
    const stateLimit = Number(form.state_timeout_ms) || 0;
    if (stateLimit !== 0 && (stateLimit < 50 || stateLimit > 5000))
      return 'State script limit: empty for the default, or 50 to 5000 ms';
    const payloadLimit = Number(form.payload_timeout_ms) || 0;
    if (payloadLimit !== 0 && (payloadLimit < 100 || payloadLimit > 10000))
      return 'Payload script limit: empty for the default, or 100 to 10000 ms';
    return '';
  }

  async function save() {
    formError = validateForm() || historyField?.validate() || '';
    if (formError) return;
    saving = true;
    let saved: MQTTDevice;
    try {
      const body = {
        name: form.name.trim(),
        connection_id: form.connection_id,
        topics: topicList(),
        state_script: form.state_script,
        control: form.control,
        options: { ...options },
        payload_script: form.payload_script,
        description: form.description.trim(),
        availability_topic: form.availability_topic.trim(),
        availability_online: form.availability_online.trim(),
        availability_offline: form.availability_offline.trim(),
        expect_every_s: Number(form.expect_every_s) || 0,
        offline_commands: form.offline_commands,
        state_qos: Number(form.state_qos) || 0,
        command_qos: Number(form.command_qos) || 0,
        command_retain: form.command_retain,
        state_timeout_ms: Number(form.state_timeout_ms) || 0,
        payload_timeout_ms: Number(form.payload_timeout_ms) || 0,
        // An entity device keeps its transport and entity; its topics and
        // scripts stay empty because the connection reads and commands it.
        ...(isEntity && editing
          ? { transport: editing.transport, entity_key: editing.entity_key, entity_type: editing.entity_type }
          : {}),
      };
      saved = editing
        ? await mqttDevicesStore.update(editing.id, body)
        : await mqttDevicesStore.create(body);
    } catch (e: any) {
      formError = e.message || 'Failed to save the device';
      saving = false;
      return;
    }
    // The device is saved by now, so a history failure keeps the dialog open
    // on the saved device and a retry does not create it twice.
    try {
      await historyField?.commit(saved.id, saved.name);
      showModal = false;
    } catch (e: any) {
      editing = saved;
      formError = `The device was saved, but its history was not: ${e.message || 'unknown error'}`;
    } finally {
      saving = false;
    }
  }

  async function regenerate() {
    if (!editing) return;
    const ok = await confirm(
      `Regenerate "${editing.name}" from Zigbee2MQTT? Its kind, options, scripts, and topics are rebuilt and hand edits to them are lost.`,
    );
    if (!ok) return;
    saving = true;
    formError = '';
    try {
      await api.mqttDevices.regenerate(editing.id);
      await mqttDevicesStore.refresh();
      showModal = false;
    } catch (e: any) {
      formError = e.message || 'The device could not be regenerated';
    } finally {
      saving = false;
    }
  }

  async function remove(device: MQTTDevice) {
    const again = device.auto_imported && autoConnectionIds.has(device.connection_id)
      ? ' Its connection imports devices automatically, so it will be added again the next time its source reports it. To keep it out, narrow the connection\'s filters or turn auto import off.'
      : '';
    const ok = await confirm(
      `Delete device "${device.name}"? Dashboard rows bound to it will stop resolving.${again}`,
    );
    if (!ok) return;
    error = '';
    try {
      await mqttDevicesStore.remove(device.id);
    } catch (e: any) {
      error = e.message || 'Failed to delete the device';
    }
  }

  function availabilityTitle(device: MQTTDevice): string {
    const by: Record<string, string> = {
      connection: 'its connection is down',
      topic: 'its availability topic',
      interval: 'its expected reporting interval',
      default: 'its connection being up',
    };
    const since = device.availability_at ? ` since ${new Date(device.availability_at).toLocaleString()}` : '';
    const reason = by[device.availability_source ?? ''] ?? '';
    return `${device.availability}${since}${reason ? `, from ${reason}` : ''}`;
  }

  function kindLabel(name: string): string {
    return kinds.find((k) => k.name === name)?.label || name || 'unset';
  }

  function connectionName(id: string): string {
    return allConnections.find((c: any) => c.id === id)?.name || 'unknown connection';
  }

  // valueSummary is what the device is holding, shown so the list says whether
  // a decoder is working without opening anything.
  function valueSummary(device: MQTTDevice): string {
    if (!device.running) return 'not running';
    const keys = Object.keys(device.state ?? {});
    if (keys.length === 0) return 'nothing decoded yet';
    return keys.map((k) => `${k}=${JSON.stringify(device.state![k])}`).join('  ');
  }

  // heardLabel says when the device's topics last carried anything, which is
  // the wiring question the decoded values cannot answer: a device that hears
  // nothing and a decoder that returns nothing look the same from the state.
  function heardLabel(device: MQTTDevice): string {
    if (!device.running) return '';
    if (!device.last_message) return 'nothing heard since start';
    const seconds = Math.max(0, Math.round((now - new Date(device.last_message).getTime()) / 1000));
    if (seconds < 10) return 'heard just now';
    if (seconds < 90) return `heard ${seconds}s ago`;
    if (seconds < 5400) return `heard ${Math.round(seconds / 60)}m ago`;
    if (seconds < 129600) return `heard ${Math.round(seconds / 3600)}h ago`;
    return `heard ${Math.round(seconds / 86400)}d ago`;
  }
</script>

<h2 class="text-xl font-bold mb-2">Devices</h2>
<p class="text-sm mb-6" style="color: var(--theme-text-muted, #a6adc8)">
  A device is the topics it speaks on and a script that reads them. The script
  returns only the values a message carried, and karakuri keeps the rest, which
  is what makes a device that reports brightness on one message and colour on
  the next readable at all. Devices that announce themselves with Home Assistant
  MQTT discovery and Zigbee2MQTT devices can be added with their scripts
  written for you, and an ESPHome node's entities, a Frigate server's camera
  switches, Home Assistant entities, and openHAB items are added as devices the
  connection reads and commands. A connection with auto import on adds its
  devices by itself. Dashboard rows bind to a device's values the way they bind
  to a control.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

{#if canWrite}
  <div class="flex flex-wrap justify-end gap-2 mb-4">
    <button class="btn btn-secondary" onclick={() => (showEntities = 'openhab')} disabled={manual(openhabConnections).length === 0}
            title={pickerTitle(openhabConnections, 'an openHAB')}>
      <i class="las la-server mr-1"></i>Add from openHAB
    </button>
    <button class="btn btn-secondary" onclick={() => (showEntities = 'homeassistant')} disabled={manual(haConnections).length === 0}
            title={pickerTitle(haConnections, 'a Home Assistant')}>
      <i class="las la-home mr-1"></i>Add from Home Assistant
    </button>
    <button class="btn btn-secondary" onclick={() => (showEntities = 'frigate')} disabled={manual(frigateConnections).length === 0}
            title={pickerTitle(frigateConnections, 'a Frigate')}>
      <i class="las la-video mr-1"></i>Add from Frigate
    </button>
    <button class="btn btn-secondary" onclick={() => (showEntities = 'esphome')} disabled={manual(esphomeConnections).length === 0}
            title={pickerTitle(esphomeConnections, 'an ESPHome')}>
      <i class="las la-microchip mr-1"></i>Add from ESPHome
    </button>
    <button class="btn btn-secondary" onclick={() => (showEntities = 'mqtt')} disabled={manual(mqttConnections).length === 0}
            title={pickerTitle(mqttConnections, 'an MQTT')}>
      <i class="las la-broadcast-tower mr-1"></i>Add from MQTT discovery
    </button>
    <button class="btn btn-secondary" onclick={() => (showDiscovery = true)} disabled={manual(mqttConnections).length === 0}
            title={pickerTitle(mqttConnections, 'an MQTT')}>
      <i class="las la-search mr-1"></i>Add from Zigbee2MQTT
    </button>
    <button class="btn btn-primary" onclick={openCreate} disabled={mqttConnections.length === 0}>
      <i class="las la-plus mr-1"></i>Add Device
    </button>
  </div>
{/if}

{#if loading}
  <p class="opacity-60">Loading devices...</p>
{:else if mqttConnections.length === 0}
  <p class="opacity-60 text-center py-8">
    No MQTT connections yet. Add one in Connections first — a device speaks on one.
  </p>
{:else if devices.length === 0}
  <p class="opacity-60 text-center py-8">
    No devices yet. Add one, then bind a dashboard row to the values it decodes.
  </p>
{:else}
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
          <th class="py-2 pr-4 text-left">Name</th>
          <th class="py-2 pr-4 text-left">Connection</th>
          <th class="py-2 pr-4 text-left">Control</th>
          <th class="py-2 pr-4 text-left">Topics</th>
          <th class="py-2 pr-4 text-left">Values</th>
          <th class="py-2 text-right">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each devices as device (device.id)}
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <td class="py-2 pr-4 font-medium">
              {device.name}
              {#if device.availability}
                <span class="avail avail-{device.availability}"
                      title={availabilityTitle(device)}>{device.availability}</span>
              {/if}
              {#if device.missing}
                <span class="avail avail-offline" title="Its source no longer lists this device.">missing</span>
              {/if}
              {#if device.auto_imported}
                <span class="avail avail-auto"
                      title={device.last_reported_at ? `Added by auto import; last reported ${new Date(device.last_reported_at).toLocaleString()}` : 'Added by auto import'}>auto</span>
              {/if}
              {#if device.description}
                <div class="text-xs opacity-60">{device.description}</div>
              {/if}
            </td>
            <td class="py-2 pr-4 text-xs opacity-70">{connectionName(device.connection_id)}</td>
            <td class="py-2 pr-4 text-xs opacity-70">
              {kindLabel(device.control)}
              {#if !device.commandable}
                <span class="opacity-60">· reports only</span>
              {/if}
            </td>
            <td class="py-2 pr-4 text-xs mono">
              {#if device.transport && device.transport in transportLabels}
                <span class="opacity-70">{transportLabels[device.transport]} {(device.entity_type ?? '').replaceAll('_', ' ')}</span>
                <div class="opacity-60">{device.entity_key}</div>
              {:else}
                {(device.topics ?? []).join(', ')}
              {/if}
            </td>
            <td class="py-2 pr-4 text-xs mono opacity-80">
              {#if device.state_error}
                <span class="text-red-400" title={device.state_error}>
                  decode failed: {device.state_error}
                </span>
              {:else}
                {valueSummary(device)}
              {/if}
              {#if device.stale}
                <div class="opacity-60" title="Restored from before the server restarted; the device has not reported since.">
                  last known, reported {device.reported_at ? new Date(device.reported_at).toLocaleString() : 'before the restart'}
                </div>
              {/if}
              {#if heardLabel(device)}
                <div class="opacity-60">{heardLabel(device)}</div>
              {/if}
              {#if device.availability_note}
                <div class="opacity-60" title="The availability topic sent a payload that is neither the online nor the offline payload.">
                  availability topic said: {device.availability_note}
                </div>
              {/if}
            </td>
            <td class="py-2 text-right whitespace-nowrap">
              {#if canWrite}
                <button class="icon-btn" title="Edit" onclick={() => openEdit(device)}>
                  <i class="la la-pen"></i>
                </button>
                <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => remove(device)}>
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

{#if showEntities}
  <EntityDiscovery kind={showEntities}
                   connections={entityConnections(showEntities)}
                   onclose={() => (showEntities = null)} />
{/if}

{#if showDiscovery}
  <Zigbee2MQTTDiscovery connections={manual(mqttConnections)} onclose={() => (showDiscovery = false)} />
{/if}

{#if showModal}
  <Modal title={editing ? (isEntity ? `Edit ${transportLabels[editing.transport ?? ''] ?? ''} Device` : 'Edit MQTT Device') : 'Add MQTT Device'} maxWidth="2xl"
         onclose={() => (showModal = false)}>
    <div class="flex flex-col gap-4">
      {#if formError}
        <div class="p-2 rounded text-sm"
             style="background-color: var(--theme-error); color: var(--theme-base);">
          {formError}
        </div>
      {/if}

      <FormField label="Name" id="device-name">
        <input id="device-name" class="input w-full" bind:value={form.name}
               placeholder="Bedroom lamp" />
      </FormField>

      <FormField label="Description" id="device-desc">
        <input id="device-desc" class="input w-full" bind:value={form.description}
               placeholder="What this device is" />
      </FormField>

      {#if isEntity}
        <p class="text-sm opacity-70">
          {kindLabel(form.control)} from the {(editing?.entity_type ?? '').replaceAll('_', ' ')} entity
          <span class="mono">{editing?.entity_key}</span> on {connectionName(form.connection_id)}. The
          connection reads and commands it, so there are no topics or scripts.
        </p>
      {:else}
      <FormField label="Connection" id="device-conn">
        <select id="device-conn" class="input w-full" bind:value={form.connection_id}>
          {#each mqttConnections as conn (conn.id)}
            <option value={conn.id}>{conn.name}</option>
          {/each}
        </select>
      </FormField>

      <FormField label="Control" id="device-control">
        <select id="device-control" class="input w-full" value={form.control}
                onchange={(e) => controlChanged(e.currentTarget.value)}>
          {#each kinds as kind (kind.name)}
            <option value={kind.name}>{kind.label}</option>
          {/each}
        </select>
        <p class="text-xs mt-1 opacity-60">
          {#if kindDef}{kindDef.description}{:else}What the device is drawn with wherever it
            appears.{/if}
        </p>
      </FormField>

      {/if}

      <!-- The kind's own settings come from the backend as a field schema, the
           same one the controls panel renders, so a new kind arrives here with
           no change. -->
      {#if kindDef && (kindDef.options ?? []).length > 0}
        <ConfigForm
          spec={kindDef.options}
          values={options}
          idPrefix="device-kind"
          onChange={setOption}
        />
      {/if}

      {#if !isEntity}
      <FormField label="Topics" id="device-topics">
        <textarea id="device-topics" class="input w-full mono" rows="3" bind:value={form.topics}
                  placeholder="zigbee2mqtt/lamp"></textarea>
        <p class="text-xs mt-1 opacity-60">
          One per line. MQTT wildcards work: <span class="mono">+</span> matches one level,
          <span class="mono">#</span> the rest.
        </p>
      </FormField>

      <FormField label="State script" id="device-decode">
        <DocumentField
          id="device-decode"
          title={`${form.name || 'Device'} state script`}
          language="javascript"
          value={form.state_script}
          emptyText="No script yet — click to write one."
          saveLabel="Save script"
          docs={{ guide: 'mqtt-device-state-script', title: 'State script reference' }}
          test={stateTest}
          onsave={(next: string) => (form.state_script = next)}
        />
        <p class="text-xs mt-1 opacity-60">
          Reads <span class="mono">topic</span>, <span class="mono">payload</span>,
          <span class="mono">meta.binary</span>, and <span class="mono">meta.state</span>. Return an
          object of the values this message carried, or nothing to ignore it.
        </p>
        {#if stateKeys.length > 0}
          <p class="text-xs mt-1 opacity-60">
            A {kindDef?.label.toLowerCase()} is drawn from
            {#each stateKeys as key, i (key.id)}<span class="mono">{key.id}</span>{#if i <
                stateKeys.length - 2}, {:else if i === stateKeys.length - 2}{' and '}{/if}{/each}.
            Any other key you return is kept and can be shown by a dashboard row on its own.
          </p>
        {/if}
      </FormField>

      <FormField label="Payload script" id="device-payload">
        <DocumentField
          id="device-payload"
          title={`${form.name || 'Device'} payload script`}
          language="javascript"
          value={form.payload_script}
          emptyText="No script yet — this device only reports."
          saveLabel="Save script"
          docs={{ guide: 'mqtt-device-payload-script', title: 'Payload script reference' }}
          test={payloadTest}
          onsave={(next: string) => (form.payload_script = next)}
        />
        <p class="text-xs mt-1 opacity-60">
          Turns a control change into a message. <span class="mono">field</span> names which of the
          keys above is being set, <span class="mono">value</span> is what it is being set to, and
          the script returns <span class="mono">{'{ topic, payload }'}</span>. Return no topic and
          the change sends nothing. Leave it empty for a device that only reports, and its rows
          draw a reading instead of something to press.
        </p>
      </FormField>

      {/if}

      <fieldset class="flex flex-col gap-3">
        <legend class="text-sm font-medium mb-1">Availability</legend>
        {#if !isEntity}
        <FormField label="Availability topic" id="device-avail-topic">
          <input id="device-avail-topic" class="input w-full mono" bind:value={form.availability_topic}
                 placeholder="tasmota/plug/LWT" />
          <p class="text-xs mt-1 opacity-60">
            Where the device says it is online or offline, usually its last-will topic. Empty means
            the device is online whenever its connection is up.
          </p>
        </FormField>
        {#if form.availability_topic.trim()}
          <div class="flex gap-3">
            <FormField label="Online payload" id="device-avail-on">
              <input id="device-avail-on" class="input w-full mono" bind:value={form.availability_online} />
            </FormField>
            <FormField label="Offline payload" id="device-avail-off">
              <input id="device-avail-off" class="input w-full mono" bind:value={form.availability_offline} />
            </FormField>
          </div>
          <p class="text-xs -mt-2 opacity-60">
            A JSON object whose <span class="mono">state</span> is one of these also matches, as
            Zigbee2MQTT sends.
          </p>
        {/if}
        {:else}
          <p class="text-xs opacity-60">
            The device is online while its node is connected, and unknown while it is not.
          </p>
        {/if}
        <FormField label="Expect a message every (seconds)" id="device-avail-every">
          <input id="device-avail-every" class="input w-full" type="number" min="0" step="1"
                 bind:value={form.expect_every_s} />
          <p class="text-xs mt-1 opacity-60">
            Mark the device offline after this long with nothing on its topics. 0 is off; otherwise
            at least 60.
          </p>
        </FormField>
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" bind:checked={form.offline_commands} />
          Send commands while offline
        </label>
        <p class="text-xs -mt-2 opacity-60">
          Off, a command to an offline device is refused. Turn it on for a sleeping battery device
          that picks up queued commands when it wakes.
        </p>
      </fieldset>

      {#if !isEntity}
        <fieldset class="flex flex-col gap-3">
          <legend class="text-sm font-medium mb-1">Delivery</legend>
          <div class="flex gap-3">
            <FormField label="State QoS" id="device-state-qos">
              <select id="device-state-qos" class="input w-full" bind:value={form.state_qos}>
                {#each QOS_LEVELS as q (q.value)}
                  <option value={q.value}>{q.label}</option>
                {/each}
              </select>
            </FormField>
            <FormField label="Command QoS" id="device-command-qos">
              <select id="device-command-qos" class="input w-full" bind:value={form.command_qos}>
                {#each QOS_LEVELS as q (q.value)}
                  <option value={q.value}>{q.label}</option>
                {/each}
              </select>
            </FormField>
          </div>
          {#if editing?.state_qos_granted !== undefined && editing.state_qos_granted !== null}
            <p class="text-xs -mt-2" style="color: var(--theme-warning, #f9e2af);">
              The broker granted QoS {editing.state_qos_granted} on the state topics, lower than asked.
            </p>
          {/if}
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" bind:checked={form.command_retain} />
            Retain commands
          </label>
          <p class="text-xs -mt-2 opacity-60">
            The broker keeps the last command and hands it to the device when it reconnects. A payload
            script can return <span class="mono">qos</span> and <span class="mono">retain</span> to
            change either for one message.
          </p>
          <div class="flex gap-3">
            <FormField label="State script limit (ms)" id="device-state-limit">
              <input id="device-state-limit" class="input w-full" type="number" min="0" step="10"
                     placeholder="250" bind:value={form.state_timeout_ms} />
            </FormField>
            <FormField label="Payload script limit (ms)" id="device-payload-limit">
              <input id="device-payload-limit" class="input w-full" type="number" min="0" step="10"
                     placeholder="2000" bind:value={form.payload_timeout_ms} />
            </FormField>
          </div>
          <p class="text-xs -mt-2 opacity-60">
            How long each script may run. Empty or 0 keeps the default; a state script may take 50 to
            5000 ms and a payload script 100 to 10000 ms.
          </p>
        </fieldset>
      {/if}

      <HistoryRecordField
        bind:this={historyField}
        sourceType="mqtt_device"
        sourceId={editing?.id ?? null}
      />

      <div class="flex gap-2 justify-end">
        {#if editing?.z2m_ieee}
          <button type="button" class="btn btn-secondary mr-auto" onclick={regenerate} disabled={saving}
                  title="Rebuild the kind, options, scripts, and topics from Zigbee2MQTT's current description of the device.">
            Regenerate from Zigbee2MQTT
          </button>
        {/if}
        <button type="button" class="btn btn-secondary" onclick={() => (showModal = false)}>
          Cancel
        </button>
        <button type="button" class="btn btn-primary" onclick={save} disabled={saving}>
          {saving ? 'Saving...' : editing ? 'Save' : 'Create'}
        </button>
      </div>
    </div>
  </Modal>
{/if}

<style>
  .avail {
    margin-left: 0.4rem;
    padding: 0 0.35rem;
    border-radius: 999px;
    font-size: 0.7rem;
    font-weight: 500;
    background: var(--theme-surface1, #45475a);
  }

  .avail-online {
    color: var(--theme-green, #a6e3a1);
  }

  .avail-offline {
    color: var(--theme-red, #f38ba8);
  }

  .avail-unknown {
    color: var(--theme-subtext0, #a6adc8);
  }

  .avail-auto {
    color: var(--theme-blue, #89b4fa);
  }

  .mono {
    font-family: var(--theme-font-mono, ui-monospace, monospace);
  }
</style>
