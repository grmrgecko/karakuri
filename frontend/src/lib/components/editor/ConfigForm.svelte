<script lang="ts">
  import type { FieldSpec } from '../../stores/nodeCatalog';
  import { controlBase, nodeCatalog } from '../../stores/nodeCatalog';
  import { connectionsStore, connectionMatchesField } from '../../stores/connections';
  import { controlsStore } from '../../stores/controls';
  import { memoryStoresStore } from '../../stores/memories';
  import { mqttDevicesStore } from '../../stores/mqttDevices';
  import { scriptGuide } from '../../stores/guides';
  import { dashboards } from '../../stores/dashboards';
  import { shebangLanguage } from '../../utils/scriptLang';
  import type { ScriptTest } from './scriptTest';
  import { api } from '../../api/client';
  import { newRuleId, operatorNeedsValue, ruleLabel, type SwitchRule } from '../../utils/switchPorts';
  import {
    fieldBindable,
    fieldPortType,
    fieldShown,
    portTypeLabel,
    scriptPortTypes,
    toolArgTypes,
    type ToolArg,
    PayloadJSON,
    PayloadYAML,
    PortBinary,
    PortString,
    type ScriptPort,
  } from '../../utils/payloadPorts';
  import DocumentField from './DocumentField.svelte';
  import ImageField from './ImageField.svelte';
  import MarkdownField from './MarkdownField.svelte';
  import ButtonField from './ButtonField.svelte';
  import FaceplateControlsField from './FaceplateControlsField.svelte';
  import { imageMarkdown, imageRefRegExp } from '../../utils/noteImages';
  import {
    changeRuleFrom,
    changeRuleOp,
    changeRuleOpPatch,
    changeRuleSearchType,
    changeSearchTypes,
    changeTargetLabel,
    changeValueIsCode,
    changeValuePlaceholder,
    changeValueSources,
    changeValueString,
    newChangeRule,
    type ChangeRule,
  } from '../../utils/changeRules';
  import { mapObject, mapRows, type MapEntry } from '../../utils/mapEntries';

  let {
    spec,
    values,
    idPrefix,
    onChange,
    params = [],
    onToggleParam,
    payloadType,
    helpAbove = false,
    scriptAnalysis = false,
  }: {
    spec: FieldSpec[];
    values: Record<string, unknown>;
    idPrefix: string;
    onChange: (fieldId: string, value: unknown) => void;
    // params lists config fields exposed as value input sockets; a field in it
    // is driven by a wire and its editor is disabled.
    params?: string[];
    onToggleParam?: (fieldId: string, exposed: boolean) => void;
    // payloadType is the port type a JSON field in this form holds a payload
    // for, which decides how its document is read: a string or binary port
    // holds the text itself rather than a JSON encoding of it. The Testing
    // section of a trigger is the case for it.
    payloadType?: string;
    // helpAbove puts a field's help between its label and its editor, for the
    // sections where the editor is a tall box and a note under it reads as
    // belonging to whatever comes next.
    helpAbove?: boolean;
    // scriptAnalysis is set only by the server flow's exact script node. All
    // other document and script callers keep the existing editor capabilities.
    scriptAnalysis?: boolean;
  } = $props();

  // isParam reports whether a field is currently supplied by a wire.
  function isParam(fieldId: string): boolean {
    return params.includes(fieldId);
  }

  // knownControls are the control editors this frontend implements; any other
  // registered control renders the editor for its base type, so a plugin's
  // custom control degrades to something still editable.
  const knownControls = new Set([
    'agent-browser',
    'password',
    'list',
    'map',
    'tokens',
    'control-picker',
    'control-list',
    'esphome-host',
    'control-attribute',
    'mqtt-device',
    'memory-store',
    'notification-endpoint',
    'dashboard-picker',
    'dashboard-widgets',
    'choices',
    'ai-model',
    'transcript',
    'openhab-item',
  ]);

  // editorFor resolves which editor a field renders with: its control when
  // implemented here, the control's registered base when not, and the field's
  // own type when it names no control at all.
  function editorFor(field: FieldSpec): string {
    if (!field.control) return field.type;
    if (knownControls.has(field.control)) return field.control;
    return controlBase(field.control) ?? field.type;
  }

  // ---- List and map controls ----
  // Both store plain JSON (an array of strings, an object of strings) so the
  // value round-trips through config like any other field.

  function listOf(fieldId: string): string[] {
    const v = values[fieldId];
    return Array.isArray(v) ? v.map((x) => String(x ?? '')) : [];
  }

  function setListItem(fieldId: string, index: number, value: string) {
    const arr = [...listOf(fieldId)];
    arr[index] = value;
    onChange(fieldId, arr);
  }

  function removeListItem(fieldId: string, index: number) {
    const arr = listOf(fieldId).filter((_, i) => i !== index);
    onChange(fieldId, arr.length > 0 ? arr : undefined);
  }

  // mapDrafts holds each map field's rows as last edited, so rows the stored
  // object cannot represent survive while a key is typed.
  let mapDrafts = $state<Record<string, MapEntry[]>>({});

  function mapOf(fieldId: string): MapEntry[] {
    return mapRows(values[fieldId], mapDrafts[fieldId]);
  }

  function setMapEntries(fieldId: string, entries: MapEntry[]) {
    mapDrafts[fieldId] = entries;
    onChange(fieldId, mapObject(entries));
  }

  // ---- Transcript control ----
  // Conversation entries in the agent's transcript format: role and message
  // rows stored in the same shape the Transcript output emits.

  interface TranscriptEntry {
    role: string;
    text: string;
  }

  const transcriptRoles = [
    { value: 'system', label: 'System' },
    { value: 'user', label: 'User' },
    { value: 'assistant', label: 'AI' },
    { value: 'tool', label: 'Tool' },
  ];

  function transcriptOf(fieldId: string): TranscriptEntry[] {
    const v = values[fieldId];
    if (!Array.isArray(v)) return [];
    return v.map((item) => {
      const entry = (item ?? {}) as Record<string, unknown>;
      return { role: String(entry.role ?? 'user'), text: String(entry.text ?? '') };
    });
  }

  function setTranscript(fieldId: string, entries: TranscriptEntry[]) {
    onChange(
      fieldId,
      entries.length > 0 ? entries.map((e) => ({ role: e.role, text: e.text })) : undefined
    );
  }

  function updateTranscript(fieldId: string, index: number, patch: Partial<TranscriptEntry>) {
    setTranscript(
      fieldId,
      transcriptOf(fieldId).map((e, i) => (i === index ? { ...e, ...patch } : e))
    );
  }

  // ---- Tokens control ----
  // Stored as the same comma-separated string the plain editor writes, so the
  // value round-trips and a frontend without the control still edits it. A
  // field with options is a closed vocabulary: the unpicked options render as
  // add chips. A field without options takes typed entries instead.

  function tokensOf(fieldId: string): string[] {
    return str(fieldId)
      .split(',')
      .map((t) => t.trim())
      .filter((t) => t !== '');
  }

  function setTokens(fieldId: string, tokens: string[]) {
    onChange(fieldId, tokens.length > 0 ? tokens.join(', ') : undefined);
  }

  function addToken(fieldId: string, token: string) {
    const t = token.trim();
    if (t === '') return;
    const cur = tokensOf(fieldId);
    if (cur.some((x) => x.toLowerCase() === t.toLowerCase())) return;
    setTokens(fieldId, [...cur, t]);
  }

  // tokenLabel shows an option's label for a stored value; a token outside
  // the vocabulary (a hand-written graph, a free-entry field) shows as-is.
  function tokenLabel(field: FieldSpec, token: string): string {
    const opt = field.options?.find((o) => o.value === token);
    return opt?.label || token;
  }

  // ---- Choices control ----
  // A choice is a value and the label it is shown under. Options and buttons
  // are the same list with different meanings: picking one is a selection,
  // pressing one is an action, and both need a wire value that can differ from
  // what a wall panel reads.

  interface Choice {
    value: string;
    label: string;
  }

  // choicesOf tolerates the plain string list an older control stored, so a
  // dropdown defined before labels existed still edits rather than emptying.
  function choicesOf(fieldId: string): Choice[] {
    const v = values[fieldId];
    if (!Array.isArray(v)) return [];
    return v.map((item) => {
      if (item && typeof item === 'object') {
        const entry = item as Record<string, unknown>;
        return { value: String(entry.value ?? ''), label: String(entry.label ?? '') };
      }
      return { value: String(item ?? ''), label: '' };
    });
  }

  function setChoices(fieldId: string, next: Choice[]) {
    onChange(
      fieldId,
      next.length > 0 ? next.map((c) => ({ value: c.value, label: c.label })) : undefined
    );
  }

  function updateChoice(fieldId: string, index: number, patch: Partial<Choice>) {
    setChoices(
      fieldId,
      choicesOf(fieldId).map((c, i) => (i === index ? { ...c, ...patch } : c))
    );
  }

  // ---- Control attribute ----

  // attributeOptions lists the parts of a control a node may address. The kind
  // comes from a sibling "kind" field when the node declares one, and
  // otherwise from whichever control the picker beside it has chosen, so a Set
  // Control node offers a light's brightness without being told twice. When a
  // specific control is chosen, attributes its options do not enable are
  // dropped — a light without colour has no colour attribute anywhere, and
  // offering one here builds a node that can only fail.
  function attributeOptions() {
    const declared = spec.some((f) => f.id === 'kind') ? values['kind'] : undefined;
    let kindName = typeof declared === 'string' ? declared : '';
    const ref = typeof values['control'] === 'string' ? (values['control'] as string) : '';
    const chosen = $controlsStore.controls.find((c) => c.id === ref);
    if (!kindName) kindName = chosen?.kind ?? '';
    const kind = $controlsStore.kinds.find((k) => k.name === kindName);
    if (!kind) return [];
    if (!chosen) return kind.attributes;
    return kind.attributes.filter((a) => {
      if (!a.requires_option) return true;
      // A missing feature flag reads as the kind's declared default, the same
      // fallback the server's optBool makes.
      const stored = chosen.options?.[a.requires_option];
      if (stored === undefined || stored === null) {
        const optSpec = kind.options.find((o) => o.id === a.requires_option);
        return optSpec?.default === true;
      }
      return stored === true || stored === 'true' || stored === 1;
    });
  }

  // notificationEndpoints reads only the approved IDs from the selected service.
  function notificationEndpoints(): { id: string; label: string }[] {
    const connection = $connectionsStore.connections.find((c) => c.id === str('connection') && c.kind === 'notification');
    if (!connection?.enabled) return [];
    const endpoints = connection.config.endpoints;
    return Array.isArray(endpoints) ? endpoints.filter((e): e is { id: string; label: string } =>
      !!e && typeof e.id === 'string' && typeof e.label === 'string') : [];
  }

  // Dashboards and their placements for the dashboard command node. The
  // placement list is read from the served layout, which is what commands
  // are checked against.
  interface DashboardOption { id: string; name: string }
  interface PlacementOption { id: string; label: string }
  let dashboardOptions = $state<DashboardOption[] | null>(null);
  let placementOptions = $state<Record<string, PlacementOption[]>>({});

  $effect(() => {
    if (dashboardOptions === null && spec.some((f) => f.control === 'dashboard-picker')) {
      dashboardOptions = [];
      dashboards.listAll().then((list) => {
        dashboardOptions = list.map((d) => ({ id: String(d.id), name: String(d.name) }));
      }).catch(() => {});
    }
  });

  $effect(() => {
    const dashboardId = str('dashboard');
    if (!dashboardId || placementOptions[dashboardId] || !spec.some((f) => f.control === 'dashboard-widgets')) return;
    placementOptions = { ...placementOptions, [dashboardId]: [] };
    api.get<any>(`/dashboards/${dashboardId}?view=deployed`).then((d) => {
      const pageTitles = new Map<string, string>((d?.pages ?? []).map((p: any) => [String(p.id), String(p.title)]));
      const options: PlacementOption[] = (d?.widgets ?? []).map((w: any) => {
        const name = w.display?.name || w.widget?.name || 'Widget';
        const where = w.region ? w.region.replace(/_/g, ' ') : `${w.x},${w.y}`;
        const page = w.page_id ? pageTitles.get(String(w.page_id)) : '';
        return { id: String(w.id), label: `${name} (${where}${page ? `, ${page}` : ''})` };
      });
      placementOptions = { ...placementOptions, [dashboardId]: options };
    }).catch(() => {});
  });

  function selectedPlacements(fieldId: string): string[] {
    return str(fieldId).split(',').map((t) => t.trim()).filter(Boolean);
  }

  function togglePlacement(fieldId: string, id: string) {
    const current = selectedPlacements(fieldId);
    const next = current.includes(id) ? current.filter((t) => t !== id) : [...current, id];
    setString(fieldId, next.join(','));
  }

  // Load the connection list once when the spec has a connection field, so
  // the dropdown can offer matching connections.
  $effect(() => {
    if (spec.some((f) => f.type === 'connection')) {
      connectionsStore.load().catch(() => {});
    }
  });

  // Load the controls when the spec picks one, so the picker has names rather
  // than the IDs the config actually stores.
  $effect(() => {
    if (spec.some((f) => f.control === 'control-picker' || f.control === 'control-list' || f.control === 'control-attribute')) {
      controlsStore.load().catch(() => {});
    }
  });

  // Load the devices when the spec picks one, the same bargain the control
  // picker makes: names in the dropdown, IDs in the config.
  $effect(() => {
    if (spec.some((f) => f.control === 'mqtt-device')) {
      mqttDevicesStore.load().catch(() => {});
    }
  });

  // Load the memory stores when the spec picks one.
  $effect(() => {
    if (spec.some((f) => f.control === 'memory-store')) {
      memoryStoresStore.load().catch(() => {});
    }
  });

  // controlOptions lists the controls a picker offers. A sibling "kind" field
  // narrows it, because that field is what the node's port type is derived
  // from: offering a control of another kind would build a node whose output
  // promise it cannot keep.
  function controlOptions() {
    const all = $controlsStore.controls;
    const kind = spec.some((f) => f.id === 'kind') ? values['kind'] : undefined;
    if (typeof kind !== 'string' || kind === '') return all;
    return all.filter((c) => c.kind === kind);
  }

  // connectionOptions lists connections matching the field's kind and public
  // config rules.
  function connectionOptions(field: FieldSpec) {
    return $connectionsStore.connections.filter((connection) =>
      connectionMatchesField(connection, field),
    );
  }

  // ---- Time zones ----
  // The browser's zone list drives the picker; the empty choice keeps the
  // documented "use the server's local time" behaviour reachable.
  const timeZones: string[] = (() => {
    try {
      const supported = (Intl as any).supportedValuesOf?.('timeZone');
      if (Array.isArray(supported) && supported.length > 0) return supported;
    } catch {
      // Older engines fall back to just the local zone below.
    }
    return [browserTimeZone()];
  })();

  function browserTimeZone(): string {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
      return 'UTC';
    }
  }

  // ---- Cron preview ----
  // Explanations come from the backend so the editor shows exactly what the
  // scheduler will do, rather than a second parser that could disagree.

  interface CronPreview {
    valid: boolean;
    error?: string;
    description?: string;
    fields?: string[];
    next?: string[];
  }

  let cronPreview = $state<Record<string, CronPreview>>({});
  let cronTimers: Record<string, ReturnType<typeof setTimeout>> = {};

  // cronLocation returns the time zone field value of this form, so the
  // preview's run times match the schedule's configured zone.
  function cronLocation(): string {
    const tzField = spec.find((f) => f.type === 'timezone');
    if (!tzField) return '';
    const v = values[tzField.id];
    return typeof v === 'string' ? v : '';
  }

  async function loadCronPreview(fieldId: string, expr: string) {
    if (!expr.trim()) {
      cronPreview[fieldId] = { valid: false };
      return;
    }
    try {
      const res = await api.nodeTypes.cronPreview<CronPreview>(expr, cronLocation());
      if (res) cronPreview[fieldId] = res;
    } catch {
      // A failed lookup simply leaves the previous explanation in place.
    }
  }

  // Debounce so typing an expression does not issue a request per keystroke.
  function queueCronPreview(fieldId: string, expr: string) {
    clearTimeout(cronTimers[fieldId]);
    cronTimers[fieldId] = setTimeout(() => void loadCronPreview(fieldId, expr), 250);
  }

  // Preview on open and whenever the expression or zone changes.
  $effect(() => {
    const location = cronLocation();
    for (const f of spec) {
      if (f.type !== 'cron') continue;
      const expr = str(f.id);
      // Referenced so the effect reruns when the zone changes.
      void location;
      queueCronPreview(f.id, expr);
    }
  });

  function formatRunTime(iso: string): string {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      timeZone: cronLocation() || undefined,
      timeZoneName: 'short',
    });
  }

  // ---- Location detection ----
  // A spec carrying both halves of a coordinate pair gets a detect button under
  // the second one, filling latitude and longitude together from the browser.

  let geoBusy = $state(false);
  let geoError = $state('');

  // geoField returns the field holding one half of the coordinate pair.
  function geoField(role: string): FieldSpec | undefined {
    return spec.find((f) => f.geo === role);
  }

  // showDetect reports whether this field is the one the detect button hangs
  // off: the longitude of a complete, editable pair.
  function showDetect(field: FieldSpec): boolean {
    if (field.geo !== 'longitude') return false;
    const lat = geoField('latitude');
    if (!lat) return false;
    return !isParam(lat.id) && !isParam(field.id);
  }

  // Coordinates are rounded to four decimals, about 10 metres. Sun events move
  // by well under a second across that distance, and a flow document has no
  // business recording where in the yard the browser was standing.
  function roundCoord(v: number): number {
    return Math.round(v * 1e4) / 1e4;
  }

  function detectLocation() {
    const lat = geoField('latitude');
    const lon = geoField('longitude');
    if (!lat || !lon) return;
    // Geolocation is a secure-context API: a plain-http install has no
    // navigator.geolocation at all, so say why rather than hanging.
    if (!navigator.geolocation) {
      geoError = 'This browser only shares location over https or on localhost.';
      return;
    }
    geoBusy = true;
    geoError = '';
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        geoBusy = false;
        onChange(lat.id, roundCoord(pos.coords.latitude));
        onChange(lon.id, roundCoord(pos.coords.longitude));
      },
      (err) => {
        geoBusy = false;
        geoError = geoErrorMessage(err);
      },
      { enableHighAccuracy: false, timeout: 10000, maximumAge: 600000 }
    );
  }

  function geoErrorMessage(err: GeolocationPositionError): string {
    switch (err.code) {
      case err.PERMISSION_DENIED:
        return 'Location permission was denied. Allow it for this site and try again.';
      case err.POSITION_UNAVAILABLE:
        return 'The browser could not determine a position.';
      case err.TIMEOUT:
        return 'Timed out waiting for a position.';
      default:
        return err.message || 'Location lookup failed.';
    }
  }

  // isShown honors show_if rules through the shared fieldShown, the same
  // evaluation port derivation uses, so a field and the socket it may carry
  // appear and vanish together.
  function isShown(field: FieldSpec): boolean {
    return fieldShown(spec, field, values);
  }

  // str renders a stored value for a text input. Objects and arrays are shown
  // as JSON rather than String()'s "[object Object]": a field that used to be
  // a JSON editor still holds a structural value until it is next edited, and
  // stringifying it that way would destroy the config on the first keystroke.
  function str(fieldId: string): string {
    const v = values[fieldId];
    if (v === undefined || v === null) return '';
    if (typeof v === 'object') return JSON.stringify(v, null, 2);
    return String(v);
  }

  // bool shows the field's default when nothing is stored, as selectValue
  // does, because the backend applies that default to an unset boolean.
  function bool(field: FieldSpec): boolean {
    const v = values[field.id];
    if (v === undefined || v === null) return field.default === true;
    return v === true;
  }

  // selectValue shows the field's default when nothing is stored, because
  // that is the value the backend will use. Showing "(none)" instead made an
  // unset select read as "no behaviour configured" when a default was in
  // force. The empty choice is only offered for selects that have no default
  // and so are genuinely optional.
  function selectValue(field: FieldSpec): string {
    const v = values[field.id];
    if (v !== undefined && v !== null && v !== '') return String(v);
    return field.default !== undefined && field.default !== null ? String(field.default) : '';
  }

  // ---- Script port declarations ----
  //
  // A port list is stored as-is on the node's config; the canvas derives the
  // node's sockets from it, so an edit here reshapes the node immediately.

  function portList(fieldId: string): ScriptPort[] {
    const v = values[fieldId];
    return Array.isArray(v) ? (v as ScriptPort[]) : [];
  }

  function commitPorts(fieldId: string, ports: ScriptPort[]) {
    onChange(fieldId, ports.length > 0 ? ports : undefined);
  }

  function addPort(fieldId: string, defaultType: string) {
    const ports = portList(fieldId);
    // Suggest a free ID so a new row is usable without renaming it first.
    const base = fieldId === 'inputs' ? 'in' : 'out';
    let id = base;
    let n = 2;
    while (ports.some((p) => p.id === id)) id = `${base}${n++}`;
    commitPorts(fieldId, [...ports, { id, label: '', type: defaultType }]);
  }

  function setPort(fieldId: string, index: number, patch: Partial<ScriptPort>) {
    const ports = portList(fieldId).map((p, i) => (i === index ? { ...p, ...patch } : p));
    commitPorts(fieldId, ports);
  }

  function removePort(fieldId: string, index: number) {
    commitPorts(fieldId, portList(fieldId).filter((_, i) => i !== index));
  }

  // ---- Tool argument declarations ----
  //
  // Same shape as the port list, one level richer: each row also carries the
  // description the model reads and whether the argument is required.

  function toolArgList(fieldId: string): ToolArg[] {
    const v = values[fieldId];
    return Array.isArray(v) ? (v as ToolArg[]) : [];
  }

  function commitToolArgs(fieldId: string, args: ToolArg[]) {
    onChange(fieldId, args.length > 0 ? args : undefined);
  }

  function addToolArg(fieldId: string) {
    const args = toolArgList(fieldId);
    let id = 'arg';
    let n = 2;
    while (args.some((a) => a.id === id)) id = `arg${n++}`;
    commitToolArgs(fieldId, [...args, { id, type: 'string' }]);
  }

  function setToolArg(fieldId: string, index: number, patch: Partial<ToolArg>) {
    commitToolArgs(fieldId, toolArgList(fieldId).map((a, i) => (i === index ? { ...a, ...patch } : a)));
  }

  function removeToolArg(fieldId: string, index: number) {
    commitToolArgs(fieldId, toolArgList(fieldId).filter((_, i) => i !== index));
  }

  function setString(fieldId: string, value: string) {
    onChange(fieldId, value === '' ? undefined : value);
  }

  function setNumber(fieldId: string, value: string) {
    if (value === '') {
      onChange(fieldId, undefined);
      return;
    }
    const n = Number(value);
    if (!Number.isNaN(n)) onChange(fieldId, n);
  }

  // ---- ESPHome node search ----

  interface FoundESPHomeNode {
    name: string;
    host: string;
    port: number;
    board?: string;
    version?: string;
    friendly_name?: string;
  }

  let esphomeNodes = $state<FoundESPHomeNode[]>([]);
  let esphomeSearching = $state(false);
  let esphomeSearched = $state(false);

  async function findESPHomeNodes() {
    esphomeSearching = true;
    try {
      const res = await api.mqttDevices.esphomeNodes<{ nodes: FoundESPHomeNode[] }>();
      esphomeNodes = res?.nodes ?? [];
    } catch {
      esphomeNodes = [];
    } finally {
      esphomeSearching = false;
      esphomeSearched = true;
    }
  }

  function pickESPHomeNode(fieldId: string, node: FoundESPHomeNode) {
    setString(fieldId, node.host);
    if (spec.some((f) => f.id === 'port')) setNumber('port', String(node.port));
    esphomeSearched = false;
  }

  // ---- Entity autocomplete ----
  // Entity fields complete against the entities of the form's selected
  // Home Assistant connection. Free text stays allowed (templates, comma
  // lists); picking an option fills the last comma-separated segment.

  interface EntityOption {
    entity_id: string;
    friendly_name?: string;
    state: string;
    area?: string;
    floor?: string;
    device?: string;
    labels?: string[];
    label_names?: string[];
  }

  let entityCache = $state<Record<string, EntityOption[]>>({});
  let entityOpen = $state<string | null>(null);
  let entityError = $state('');

  // entityConnectionId returns the value of the form's HA connection field.
  function entityConnectionId(): string {
    const connField = spec.find(
      (f) => f.type === 'connection' && f.connection_kind === 'homeassistant',
    );
    if (!connField) return '';
    const v = values[connField.id];
    return typeof v === 'string' ? v : '';
  }

  async function loadEntities(connId: string) {
    if (!connId || entityCache[connId]) return;
    entityError = '';
    try {
      entityCache[connId] = (await api.connections.haEntities<EntityOption[]>(connId)) || [];
    } catch (e: any) {
      entityCache[connId] = [];
      entityError = e.message || 'Failed to load entities';
    }
  }

  // entityTerm is the text being completed: the last comma-separated segment.
  function entityTerm(fieldId: string): string {
    const v = str(fieldId);
    const idx = v.lastIndexOf(',');
    return (idx >= 0 ? v.slice(idx + 1) : v).trim().toLowerCase();
  }

  function filteredEntities(fieldId: string): EntityOption[] {
    const list = entityCache[entityConnectionId()] ?? [];
    const term = entityTerm(fieldId);
    // The server sends the list grouped by floor and area, so a search keeps
    // that order and the headings stay meaningful.
    const matches = term
      ? list.filter((e) =>
          [e.entity_id, e.friendly_name, e.area, e.floor, e.device, ...(e.labels ?? []), ...(e.label_names ?? [])].some((v) =>
            (v ?? '').toLowerCase().includes(term),
          ),
        )
      : list;
    return matches.slice(0, 80);
  }

  // entityGroup is the heading an entity sits under in the dropdown.
  function entityGroup(e: EntityOption): string {
    if (!e.area) return 'No area';
    return e.floor ? `${e.floor} · ${e.area}` : e.area;
  }

  // ---- openHAB item pickers ----
  // An openhab-item field holds item names, comma-separated, completed
  // against the items of the form's openHAB connection and grouped by
  // location. Typed text stays allowed.

  interface OpenHABItem {
    name: string;
    label: string;
    type: string;
    state: string;
    location_label?: string;
    floor_label?: string;
  }

  let openhabCache = $state<Record<string, OpenHABItem[]>>({});
  let openhabOpen = $state<string | null>(null);
  let openhabError = $state('');

  function openhabConnectionId(): string {
    const connField = spec.find((f) => f.type === 'connection' && f.connection_kind === 'openhab');
    if (!connField) return '';
    const v = values[connField.id];
    return typeof v === 'string' ? v : '';
  }

  async function loadOpenHABItems(connId: string) {
    if (!connId || openhabCache[connId]) return;
    openhabError = '';
    try {
      openhabCache[connId] = (await api.connections.openhabItems<OpenHABItem[]>(connId)) || [];
    } catch (e: any) {
      openhabCache[connId] = [];
      openhabError = e.message || 'Failed to load items';
    }
  }

  function filteredOpenHABItems(fieldId: string): OpenHABItem[] {
    const list = openhabCache[openhabConnectionId()] ?? [];
    const term = entityTerm(fieldId);
    const matches = term
      ? list.filter((i) =>
          [i.name, i.label, i.location_label, i.floor_label, i.type].some((v) =>
            (v ?? '').toLowerCase().includes(term),
          ),
        )
      : list;
    return matches.slice(0, 80);
  }

  function openhabGroup(i: OpenHABItem): string {
    if (!i.location_label) return 'No location';
    return i.floor_label && i.floor_label !== i.location_label
      ? `${i.floor_label} · ${i.location_label}`
      : i.location_label;
  }

  // ---- Home Assistant target pickers ----
  // An ha-target field holds comma-separated registry IDs, completed against
  // the connection's floors, areas, devices, or labels. Typed text and {{ }}
  // expressions stay allowed, as in an entity field.

  interface RegistryItem {
    id: string;
    name: string;
    detail?: string;
  }
  interface RegistryData {
    floors: { floor_id: string; name: string }[];
    areas: { area_id: string; name: string; floor_id?: string }[];
    devices: { id: string; name: string; area_id?: string; model?: string }[];
    labels: { label_id: string; name: string }[];
  }

  let registryCache = $state<Record<string, RegistryData>>({});
  let targetOpen = $state<string | null>(null);
  let targetError = $state('');

  async function loadRegistry(connId: string) {
    if (!connId || registryCache[connId]) return;
    targetError = '';
    try {
      registryCache[connId] = (await api.connections.haRegistry<RegistryData>(connId)) || {
        floors: [],
        areas: [],
        devices: [],
        labels: [],
      };
    } catch (e: any) {
      targetError = e.message || 'Failed to load areas';
    }
  }

  // registryItems lists one registry as picker entries.
  function registryItems(control: string): RegistryItem[] {
    const reg = registryCache[entityConnectionId()];
    if (!reg) return [];
    const areaName = (id?: string) => reg.areas.find((a) => a.area_id === id)?.name ?? '';
    const floorName = (id?: string) => reg.floors.find((f) => f.floor_id === id)?.name ?? '';
    switch (control) {
      case 'floor':
        return reg.floors.map((f) => ({ id: f.floor_id, name: f.name }));
      case 'device':
        return reg.devices.map((d) => ({ id: d.id, name: d.name, detail: areaName(d.area_id) }));
      case 'label':
        return reg.labels.map((l) => ({ id: l.label_id, name: l.name }));
      default:
        return reg.areas.map((a) => ({ id: a.area_id, name: a.name, detail: floorName(a.floor_id) }));
    }
  }

  function filteredTargets(fieldId: string, control: string): RegistryItem[] {
    const term = entityTerm(fieldId);
    const chosen = new Set(
      str(fieldId)
        .split(',')
        .map((p) => p.trim())
        .filter(Boolean),
    );
    return registryItems(control)
      .filter((i) => !chosen.has(i.id))
      .filter(
        (i) =>
          !term ||
          i.name.toLowerCase().includes(term) ||
          i.id.toLowerCase().includes(term) ||
          (i.detail ?? '').toLowerCase().includes(term),
      )
      .slice(0, 50);
  }

  // targetNames reads the chosen IDs back as names, so a stored list of
  // registry IDs says which rooms it means.
  function targetNames(fieldId: string, control: string): string {
    const items = registryItems(control);
    return str(fieldId)
      .split(',')
      .map((p) => p.trim())
      .filter(Boolean)
      .map((id) => items.find((i) => i.id === id)?.name ?? id)
      .join(', ');
  }

  // ---- AI model autocomplete ----
  // The ai-model control completes against the models the form's selected AI
  // connection lists (the server caches the catalog briefly). Free text stays
  // allowed: providers accept models their listing omits.

  let aiModelCache = $state<Record<string, string[]>>({});
  let aiModelOpen = $state<string | null>(null);
  let aiModelError = $state('');

  // aiConnectionId returns the value of the form's connection field. The
  // control only appears on nodes whose connection is an AI provider, so the
  // first connection field is the one.
  function aiConnectionId(): string {
    const connField = spec.find((f) => f.type === 'connection');
    if (!connField) return '';
    const v = values[connField.id];
    return typeof v === 'string' ? v : '';
  }

  async function loadAIModels(connId: string) {
    if (!connId || aiModelCache[connId]) return;
    aiModelError = '';
    try {
      aiModelCache[connId] = (await api.connections.aiModels<string[]>(connId)) || [];
    } catch (e: any) {
      aiModelCache[connId] = [];
      aiModelError = e.message || 'Failed to list models';
    }
  }

  function filteredAIModels(fieldId: string): string[] {
    const list = aiModelCache[aiConnectionId()] ?? [];
    const term = str(fieldId).trim().toLowerCase();
    const matches = term ? list.filter((m) => m.toLowerCase().includes(term)) : list;
    return matches.slice(0, 50);
  }

  // ---- Document fields ----
  //
  // Every multi-line value — a script, a template, a JSON body, a trigger's
  // test payload — is edited in the same document editor rather than a
  // textarea, because the config panel is far too narrow to write any of them
  // in. What differs per field is the mode it is highlighted in and how its
  // text is read back into the config, which is what these decide.

  // isDocument reports whether a field is edited as a document.
  function isDocument(type: string): boolean {
    return type === 'script' || type === 'json' || type === 'text';
  }

  // documentLanguage picks the editor mode. A script names the config field
  // holding its language. A text field may name one holding the type its text
  // is read as, and JSON and YAML are the ones of those with a mode of their
  // own. A JSON field is a JSON document unless the port it holds a payload
  // for says the text is read as itself.
  // A script field can be run against a made-up message. The sample is the
  // message shape a script node receives, so what is typed here is what the
  // script reads — and the seeded document doubles as a reminder of the names
  // in scope.
  function scriptTest(language: string): ScriptTest {
    return {
      title: 'Test the script',
      hint: 'The sample is the message the script receives. Variable writes are reported, not saved.',
      sample: JSON.stringify(
        { payload: { example: 1 }, topic: 'sensors/office', meta: {} },
        null,
        2,
      ),
      run: async (source, input) => {
        const sample = (input ?? {}) as Record<string, unknown>;
        return api.scripts.test<{ value?: unknown; output?: string[]; error?: string }>({
          language,
          source,
          payload: sample.payload,
          topic: sample.topic ?? '',
          meta: sample.meta,
          input: sample.input,
          vars: sample.vars,
          flow: sample.flow,
          global: sample.global,
        });
      },
    };
  }

  function documentLanguage(field: FieldSpec): string {
    if (field.type === 'script') return scriptLanguage(field);
    if (field.type === 'json') {
      return payloadType === PortString || payloadType === PortBinary ? 'plain' : 'json';
    }
    switch (readAs(field)) {
      case PayloadJSON:
        return 'json';
      case PayloadYAML:
        return 'yaml';
      default:
        return 'plain';
    }
  }

  // readAs returns the value of the config field naming how this field's text
  // is read, falling back to that field's default so a config saved before the
  // setting existed is read the way it was written.
  function readAs(field: FieldSpec): string {
    if (!field.language_field) return '';
    const value = values[field.language_field];
    if (typeof value === 'string' && value !== '') return value;
    const named = specFor(field.language_field);
    return typeof named?.default === 'string' ? named.default : '';
  }

  // documentText renders the stored value as the document the editor opens on.
  function documentText(field: FieldSpec): string {
    if (field.type === 'script') return scriptSource(field);
    if (field.type !== 'json') return str(field.id);
    const value = values[field.id];
    if (value === undefined || value === null) return '';
    if (documentLanguage(field) === 'plain' && typeof value === 'string') return value;
    return JSON.stringify(value, null, 2);
  }

  // commitDocument stores an edited document. A JSON document is parsed, so
  // the config holds the value the node will be handed rather than its text.
  function commitDocument(field: FieldSpec, text: string) {
    if (field.type === 'script') {
      onChange(field.id, text);
      return;
    }
    if (field.type !== 'json') {
      setString(field.id, text);
      return;
    }
    if (text.trim() === '') {
      onChange(field.id, undefined);
      return;
    }
    onChange(field.id, documentLanguage(field) === 'plain' ? text : JSON.parse(text));
  }

  // documentProblem is the editor's save check: a document that does not parse
  // is reported there rather than dropped when the editor closes.
  function documentProblem(field: FieldSpec, text: string): string | null {
    if (field.type !== 'json' || documentLanguage(field) !== 'json') return null;
    if (text.trim() === '') return null;
    try {
      JSON.parse(text);
      return null;
    } catch (e: any) {
      return e.message || 'Invalid JSON';
    }
  }

  // ---- Attached images ----
  //
  // Images live in their own config field, and the document that displays them
  // refers to one by name. Attaching an image writes the reference into that
  // document and detaching takes it back out, so the picture appears where it
  // was attached and nothing is left pointing at an image that is gone. The
  // target is the form's first text field, which is the note's markdown.

  function markdownField(): FieldSpec | undefined {
    return spec.find((f) => f.type === 'text');
  }

  function referenceImage(name: string) {
    const field = markdownField();
    if (!field) return;
    const text = str(field.id);
    const reference = imageMarkdown(name);
    if (text.includes(reference)) return;
    setString(field.id, text.trim() === '' ? reference : `${text.replace(/\s+$/, '')}\n\n${reference}`);
  }

  function dereferenceImage(name: string) {
    const field = markdownField();
    if (!field) return;
    const text = str(field.id);
    const next = text.replace(imageRefRegExp(name), '').replace(/\n{3,}/g, '\n\n');
    if (next !== text) setString(field.id, next);
  }

  // scriptLanguage reads the language from the field this script is bound to,
  // falling back to that field's default so a config saved before a language
  // was chosen still highlights sensibly.
  function scriptLanguage(field: FieldSpec): string {
    // A script with no language field picks its language from the shebang, the
    // way the agent's run-script node does; a shebang-less draft stays plain.
    if (!field.language_field) {
      const source = scriptSource(field);
      return shebangLanguage(source) ?? 'plain';
    }
    const value = values[field.language_field];
    if (typeof value === 'string' && value !== '') return value;
    const spec = specFor(field.language_field);
    return typeof spec?.default === 'string' ? spec.default : 'javascript';
  }

  function specFor(fieldId: string): FieldSpec | undefined {
    return spec.find((f) => f.id === fieldId);
  }

  // scriptSource returns the current source, falling back to the starter
  // example for the chosen language when the field has never been filled in.
  function scriptSource(field: FieldSpec): string {
    const value = values[field.id];
    if (typeof value === 'string' && value.trim() !== '') return value;
    return field.examples?.[scriptLanguage(field)] ?? '';
  }

  // isUntouched reports whether a script field still holds a starter example
  // rather than something the user wrote. An empty field counts, so the two
  // cases the example may be replaced in are one test.
  function isUntouched(field: FieldSpec, source: string): boolean {
    const trimmed = source.trim();
    if (trimmed === '') return true;
    return Object.values(field.examples ?? {}).some((ex) => ex.trim() === trimmed);
  }

  // Keep an untouched script field on the example for its language: prefill an
  // empty field so the editor opens on working code rather than a blank page,
  // and swap the example when the language picker moves. A script the user has
  // edited never matches an example, so it is left alone.
  $effect(() => {
    for (const field of spec) {
      if (field.type !== 'script') continue;
      const example = field.examples?.[scriptLanguage(field)];
      if (!example) continue;
      const current = values[field.id];
      const source = typeof current === 'string' ? current : '';
      if (source === example || !isUntouched(field, source)) continue;
      onChange(field.id, example);
    }
  });

  // ---- Rule lists ----
  // The switch node's routing rules and the change node's rewrite rules are
  // both ordered lists edited row by row, so they share these helpers. Switch
  // rules carry a stable ID so reordering or deleting one never re-points the
  // edges of the rules around it; change rules apply in order and own no
  // ports, so their position is all that identifies them.

  function rulesOf<T>(fieldId: string): T[] {
    const v = values[fieldId];
    return Array.isArray(v) ? (v as T[]) : [];
  }

  function setRules(fieldId: string, rules: unknown[]) {
    onChange(fieldId, rules);
  }

  function addRule(fieldId: string, rule: unknown) {
    setRules(fieldId, [...rulesOf(fieldId), rule]);
  }

  function updateRule<T>(fieldId: string, index: number, patch: Partial<T>) {
    setRules(
      fieldId,
      rulesOf<T>(fieldId).map((r, i) => (i === index ? { ...r, ...patch } : r)),
    );
  }

  function removeRule(fieldId: string, index: number) {
    setRules(
      fieldId,
      rulesOf(fieldId).filter((_, i) => i !== index),
    );
  }

  // moveRule swaps a rule with its neighbour. For a switch that reorders the
  // output ports on the canvas; for a change it reorders the rewrites.
  function moveRule(fieldId: string, index: number, delta: number) {
    const rules = [...rulesOf(fieldId)];
    const to = index + delta;
    if (to < 0 || to >= rules.length) return;
    [rules[index], rules[to]] = [rules[to], rules[index]];
    setRules(fieldId, rules);
  }

  function rulePlaceholder(operator: string): string {
    switch (operator) {
      case 'expression':
        return 'payload.temperature > 21';
      case 'regex':
        return '^light\\.';
      default:
        return 'value';
    }
  }

  function pickEntity(fieldId: string, entityId: string) {
    const v = str(fieldId);
    const idx = v.lastIndexOf(',');
    const next = idx >= 0 ? `${v.slice(0, idx + 1)} ${entityId}` : entityId;
    onChange(fieldId, next);
    entityOpen = null;
  }
</script>

<div class="flex flex-col gap-3">
  {#each spec as field (field.id)}
    <!-- A port field is a permanent socket on the card, not a setting: there
         is nothing to type, so the form renders nothing for it. -->
    {#if isShown(field) && !field.port && field.control !== 'hidden'}
      {@const inputId = `${idPrefix}-${field.id}`}
      {@const editor = editorFor(field)}
      {@const canToggleParam = !!onToggleParam && !field.no_param && fieldBindable(field.type)}
      <div>
        {#if !field.hide_label || canToggleParam}
          <div class="flex items-center justify-between mb-1 gap-2">
            {#if !field.hide_label}
              <label for={inputId} class="block text-xs font-medium">
                {field.label}
                {#if field.required}<span style="color: var(--theme-error);">*</span>{/if}
              </label>
            {/if}
            {#if canToggleParam}
            <button
              type="button"
              class="param-toggle"
              class:on={isParam(field.id)}
              title={isParam(field.id)
                ? 'Supplied by a wire. Click to go back to a typed-in value.'
                : `Expose as a ${portTypeLabel(fieldPortType(field.type) ?? '').toLowerCase()} input so another node supplies it`}
              aria-pressed={isParam(field.id)}
              onclick={() => onToggleParam(field.id, !isParam(field.id))}
            >
              <i class="las la-plug"></i>
              {isParam(field.id) ? 'wired' : 'input'}
            </button>
            {/if}
          </div>
        {/if}

        {#if field.help && helpAbove}
          <p class="text-xs mb-1 opacity-50">{field.help}</p>
        {/if}

        <!-- A wired field is supplied by its value edge, so its editor is
             disabled rather than offering a second value nothing reads. -->
        <fieldset class="field-editor" disabled={isParam(field.id)}>
        {#if editor === 'esphome-host'}
          <div class="flex gap-2">
            <input
              id={inputId}
              class="input w-full text-sm"
              placeholder={field.placeholder || ''}
              value={str(field.id)}
              oninput={(e) => setString(field.id, e.currentTarget.value)}
            />
            <button type="button" class="btn btn-secondary text-sm whitespace-nowrap"
                    disabled={esphomeSearching} onclick={findESPHomeNodes}>
              {esphomeSearching ? 'Searching...' : 'Find nodes'}
            </button>
          </div>
          {#if esphomeSearched}
            {#if esphomeNodes.length === 0}
              <p class="text-xs mt-1 opacity-60">
                No ESPHome nodes answered on this network. Type the node's address instead.
              </p>
            {:else}
              <div class="flex flex-col gap-1 mt-1">
                {#each esphomeNodes as node (node.name)}
                  <button type="button" class="text-left text-sm px-2 py-1 rounded node-pick"
                          onclick={() => pickESPHomeNode(field.id, node)}>
                    <span class="font-medium">{node.friendly_name || node.name}</span>
                    <span class="opacity-60"> {node.host}:{node.port}{node.board ? ` · ${node.board}` : ''}{node.version ? ` · ${node.version}` : ''}</span>
                  </button>
                {/each}
              </div>
            {/if}
          {/if}
        {:else if editor === 'password'}
          <input
            id={inputId}
            type="password"
            class="input w-full text-sm"
            placeholder={field.placeholder || ''}
            autocomplete="new-password"
            value={str(field.id)}
            oninput={(e) => setString(field.id, e.currentTarget.value)}
          />
        {:else if editor === 'transcript'}
          {@const entries = transcriptOf(field.id)}
          <div class="flex flex-col gap-1">
            {#each entries as entry, i (i)}
              <div class="flex gap-1 items-start">
                <select
                  class="input text-sm w-24 shrink-0"
                  value={entry.role}
                  onchange={(e) => updateTranscript(field.id, i, { role: e.currentTarget.value })}
                >
                  {#each transcriptRoles as role (role.value)}
                    <option value={role.value}>{role.label}</option>
                  {/each}
                </select>
                <div class="flex-1 min-w-0">
                  <DocumentField
                    id={`${inputId}-${i}`}
                    title={`Transcript message ${i + 1}`}
                    language="plain"
                    value={entry.text}
                    emptyText="Empty message. Click to edit."
                    saveLabel="Save message"
                    previewLineCount={1}
                    onsave={(text: string) => updateTranscript(field.id, i, { text })}
                  />
                </div>
                <button
                  type="button"
                  class="btn btn-danger text-xs px-2 py-1"
                  aria-label="Remove message"
                  onclick={() => setTranscript(field.id, entries.filter((_, j) => j !== i))}
                >
                  <i class="las la-times"></i>
                </button>
              </div>
            {/each}
            <button
              type="button"
              id={inputId}
              class="btn btn-secondary text-xs px-2 py-1 self-start"
              onclick={() => setTranscript(field.id, [...entries, { role: 'user', text: '' }])}
            >
              <i class="las la-plus mr-1"></i>Add message
            </button>
          </div>
        {:else if editor === 'tokens'}
          {@const selected = tokensOf(field.id)}
          {@const available = (field.options ?? []).filter(
            (o) => !selected.some((t) => t.toLowerCase() === o.value.toLowerCase())
          )}
          <div class="token-box">
            {#each selected as token (token)}
              <span class="token">
                {tokenLabel(field, token)}
                <button
                  type="button"
                  class="token-x"
                  aria-label={`Remove ${token}`}
                  onclick={() => setTokens(field.id, selected.filter((t) => t !== token))}
                >
                  <i class="las la-times"></i>
                </button>
              </span>
            {/each}
            {#each available as opt, ai (opt.value)}
              <button
                type="button"
                id={ai === 0 && selected.length === 0 ? inputId : undefined}
                class="token token-add"
                onclick={() => addToken(field.id, opt.value)}
              >
                <i class="las la-plus"></i>
                {opt.label || opt.value}
              </button>
            {/each}
            {#if !field.options?.length}
              <input
                id={inputId}
                class="token-input"
                placeholder={field.placeholder || 'Add…'}
                onkeydown={(e) => {
                  if (e.key === 'Enter' || e.key === ',') {
                    e.preventDefault();
                    addToken(field.id, e.currentTarget.value);
                    e.currentTarget.value = '';
                  } else if (
                    e.key === 'Backspace' &&
                    e.currentTarget.value === '' &&
                    selected.length > 0
                  ) {
                    setTokens(field.id, selected.slice(0, -1));
                  }
                }}
                onblur={(e) => {
                  addToken(field.id, e.currentTarget.value);
                  e.currentTarget.value = '';
                }}
              />
            {/if}
          </div>
        {:else if editor === 'list'}
          {@const items = listOf(field.id)}
          <div class="flex flex-col gap-1">
            {#each items as item, i (i)}
              <div class="flex gap-1 items-center">
                <input
                  class="input text-sm flex-1 min-w-0"
                  placeholder={field.placeholder || ''}
                  value={item}
                  oninput={(e) => setListItem(field.id, i, e.currentTarget.value)}
                />
                <button
                  type="button"
                  class="btn btn-danger text-xs px-2 py-1"
                  aria-label="Remove entry"
                  onclick={() => removeListItem(field.id, i)}
                >
                  <i class="las la-times"></i>
                </button>
              </div>
            {/each}
            <button
              type="button"
              id={inputId}
              class="btn btn-secondary text-xs px-2 py-1 self-start"
              onclick={() => onChange(field.id, [...items, ''])}
            >
              <i class="las la-plus mr-1"></i>Add entry
            </button>
          </div>
        {:else if editor === 'map'}
          {@const entries = mapOf(field.id)}
          <div class="flex flex-col gap-1">
            {#each entries as [key, value], i (i)}
              <div class="flex gap-1 items-center">
                <input
                  class="input text-sm flex-1 min-w-0 font-mono"
                  placeholder="key"
                  value={key}
                  oninput={(e) =>
                    setMapEntries(
                      field.id,
                      entries.map(([k, v], j) => (j === i ? [e.currentTarget.value, v] : [k, v]))
                    )}
                />
                <input
                  class="input text-sm flex-1 min-w-0"
                  placeholder="value"
                  value={value}
                  oninput={(e) =>
                    setMapEntries(
                      field.id,
                      entries.map(([k, v], j) => (j === i ? [k, e.currentTarget.value] : [k, v]))
                    )}
                />
                <button
                  type="button"
                  class="btn btn-danger text-xs px-2 py-1"
                  aria-label="Remove entry"
                  onclick={() => setMapEntries(field.id, entries.filter((_, j) => j !== i))}
                >
                  <i class="las la-times"></i>
                </button>
              </div>
            {/each}
            <button
              type="button"
              id={inputId}
              class="btn btn-secondary text-xs px-2 py-1 self-start"
              onclick={() => setMapEntries(field.id, [...entries, ['', '']])}
            >
              <i class="las la-plus mr-1"></i>Add entry
            </button>
          </div>
        {:else if editor === 'choices'}
          {@const choices = choicesOf(field.id)}
          <div class="flex flex-col gap-1">
            {#each choices as choice, i (i)}
              <div class="flex gap-1 items-center">
                <input
                  class="input text-sm flex-1 min-w-0 font-mono"
                  placeholder="value"
                  value={choice.value}
                  oninput={(e) => updateChoice(field.id, i, { value: e.currentTarget.value })}
                />
                <input
                  class="input text-sm flex-1 min-w-0"
                  placeholder="label"
                  value={choice.label}
                  oninput={(e) => updateChoice(field.id, i, { label: e.currentTarget.value })}
                />
                <button
                  type="button"
                  class="btn btn-danger text-xs px-2 py-1"
                  aria-label="Remove choice"
                  onclick={() => setChoices(field.id, choices.filter((_, j) => j !== i))}
                >
                  <i class="las la-times"></i>
                </button>
              </div>
            {/each}
            <button
              type="button"
              id={inputId}
              class="btn btn-secondary text-xs px-2 py-1 self-start"
              onclick={() => setChoices(field.id, [...choices, { value: '', label: '' }])}
            >
              <i class="las la-plus mr-1"></i>Add choice
            </button>
          </div>
          <p class="text-xs mt-1 opacity-50">
            The value is what flows and devices read. An empty label shows the value itself.
          </p>
        {:else if editor === 'control-attribute'}
          {@const attributes = attributeOptions()}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={str(field.id)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            <option value="">Whole value</option>
            {#each attributes as attribute (attribute.id)}
              <option value={attribute.id}>
                {attribute.label}{attribute.read_only
                  ? ' (reports only)'
                  : attribute.write_only
                    ? ' (action)'
                    : ''}
              </option>
            {/each}
          </select>
          {#if attributes.length <= 1}
            <p class="text-xs mt-1 opacity-50">
              This kind holds a single value, so there is nothing to narrow to.
            </p>
          {/if}
        {:else if editor === 'control-picker'}
          {@const options = controlOptions()}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={str(field.id)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            <option value="">(select a control)</option>
            {#each options as control (control.id)}
              <option value={control.id}>{control.name}</option>
            {/each}
          </select>
          {#if $controlsStore.loaded && options.length === 0}
            <p class="text-xs mt-1 opacity-50">
              No matching controls. Define one in Settings → Controls.
            </p>
          {/if}
        {:else if editor === 'control-list'}
          {@const chosen = selectedPlacements(field.id)}
          {@const primary = str('control')}
          <div class="flex flex-col gap-1">
            {#each $controlsStore.controls.filter((c) => c.id !== primary) as control (control.id)}
              <label class="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={chosen.includes(control.id)} onchange={() => togglePlacement(field.id, control.id)} />
                <span class="truncate">{control.name}</span>
              </label>
            {/each}
            {#each chosen.filter((id) => !$controlsStore.controls.some((c) => c.id === id)) as missing (missing)}
              <label class="flex items-center gap-2 text-sm opacity-60">
                <input type="checkbox" checked onchange={() => togglePlacement(field.id, missing)} />
                <span class="truncate">{missing} (no such control)</span>
              </label>
            {/each}
          </div>
        {:else if editor === 'dashboard-picker'}
          <select id={inputId} class="input w-full text-sm" value={str(field.id)} onchange={(e) => setString(field.id, e.currentTarget.value)}>
            <option value="">(select a dashboard)</option>
            {#if str(field.id) && !(dashboardOptions ?? []).some((d) => d.id === str(field.id))}
              <option value={str(field.id)}>{str(field.id)}</option>
            {/if}
            {#each dashboardOptions ?? [] as d (d.id)}
              <option value={d.id}>{d.name}</option>
            {/each}
          </select>
        {:else if editor === 'dashboard-widgets'}
          {@const options = placementOptions[str('dashboard')] ?? []}
          {@const chosen = selectedPlacements(field.id)}
          {#if !str('dashboard')}
            <p class="text-xs opacity-50">Select a dashboard first.</p>
          {:else}
            <div class="flex flex-col gap-1">
              {#each options as option (option.id)}
                <label class="flex items-center gap-2 text-sm">
                  <input type="checkbox" checked={chosen.includes(option.id)} onchange={() => togglePlacement(field.id, option.id)} />
                  <span class="truncate">{option.label}</span>
                </label>
              {/each}
              {#each chosen.filter((id) => !options.some((o) => o.id === id)) as missing (missing)}
                <label class="flex items-center gap-2 text-sm opacity-60">
                  <input type="checkbox" checked onchange={() => togglePlacement(field.id, missing)} />
                  <span class="truncate">{missing} (not on the served layout)</span>
                </label>
              {/each}
              {#if options.length === 0}
                <p class="text-xs opacity-50">The dashboard's served layout has no widgets.</p>
              {/if}
            </div>
          {/if}
        {:else if editor === 'notification-endpoint'}
          {@const endpoints = notificationEndpoints()}
          <select id={inputId} class="input w-full text-sm" value={str(field.id)} onchange={(e) => setString(field.id, e.currentTarget.value)}>
            <option value="">(select an allowed endpoint)</option>
            {#if str(field.id) && !endpoints.some((e) => e.id === str(field.id))}
              <option value={str(field.id)}>{str(field.id)} (unavailable)</option>
            {/if}
            {#each endpoints as endpoint (endpoint.id)}
              <option value={endpoint.id}>{endpoint.label}</option>
            {/each}
          </select>
          {#if endpoints.length === 0}
            <p class="text-xs mt-1 opacity-50">Select an enabled service with allowed endpoints in Settings → Notifications.</p>
          {/if}
        {:else if editor === 'memory-store'}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={str(field.id)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            <option value="">(select a memory store)</option>
            {#each $memoryStoresStore.stores as memoryStore (memoryStore.id)}
              <option value={memoryStore.id}>{memoryStore.name}</option>
            {/each}
          </select>
          {#if $memoryStoresStore.loaded && $memoryStoresStore.stores.length === 0}
            <p class="text-xs mt-1 opacity-50">
              No memory stores. Define one in Settings → Memory.
            </p>
          {/if}
        {:else if editor === 'mqtt-device'}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={str(field.id)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            <option value="">(select a device)</option>
            {#each $mqttDevicesStore.devices as device (device.id)}
              <option value={device.id}>{device.name}</option>
            {/each}
          </select>
          {#if $mqttDevicesStore.loaded && $mqttDevicesStore.devices.length === 0}
            <p class="text-xs mt-1 opacity-50">
              No MQTT devices. Define one in Settings → MQTT Devices.
            </p>
          {/if}
        {:else if editor === 'agent-browser'}
          <select id={inputId} class="input w-full text-sm" value={str(field.id)} onchange={(e) => setString(field.id, e.currentTarget.value)}>
            <option value="">Select an agent</option>
            {#each Object.values($nodeCatalog.byType).filter((meta) => /^agent\.[^.]+\.open-url$/.test(meta.type)) as agent (agent.type)}
              <option value={agent.type.split('.')[1]}>{agent.group}</option>
            {/each}
          </select>
        {:else if field.type === 'markdown'}
          <MarkdownField id={inputId} title={field.label} value={str(field.id)} attachments={field.attachment_field ? values[field.attachment_field] : {}}
            onsave={(text, files) => { if (field.attachment_field) onChange(field.attachment_field, files); setString(field.id, text); }} />
        {:else if field.type === 'attachments'}
          <p class="text-xs text-text-muted">Manage images and files in the Markdown editor.</p>
        {:else if field.type === 'buttons'}
          <ButtonField value={values[field.id]} onChange={(buttons) => onChange(field.id, buttons)} />
        {:else if field.type === 'faceplate_controls'}
          <FaceplateControlsField value={values[field.id]} onChange={(controls) => onChange(field.id, controls)} />
        {:else if isDocument(field.type)}
          <DocumentField
            id={inputId}
            title={field.label}
            language={documentLanguage(field)}
            value={documentText(field)}
            emptyText={field.placeholder || 'Empty — click to edit.'}
            saveLabel={field.type === 'script' ? 'Save script' : 'Save'}
            hint={field.type === 'script'
              ? 'Ctrl+Space completes · Ctrl+Enter saves'
              : 'Ctrl+Enter saves'}
            docs={field.type === 'script' ? scriptGuide(documentLanguage(field)) : null}
            test={field.type === 'script' ? scriptTest(documentLanguage(field)) : null}
            scriptAnalysis={scriptAnalysis && field.type === 'script'}
            validate={(next: string) => documentProblem(field, next)}
            onsave={(next: string) => commitDocument(field, next)}
          />
        {:else if field.type === 'number'}
          <input
            id={inputId}
            type="number"
            step="any"
            class="input w-full text-sm"
            placeholder={field.placeholder || ''}
            value={str(field.id)}
            oninput={(e) => setNumber(field.id, e.currentTarget.value)}
          />
          {#if showDetect(field)}
            <button type="button" class="geo-detect" disabled={geoBusy} onclick={detectLocation}>
              <i class="las {geoBusy ? 'la-spinner la-spin' : 'la-crosshairs'}"></i>
              {geoBusy ? 'Detecting…' : 'Detect location'}
            </button>
            {#if geoError}
              <p class="text-xs mt-1" style="color: var(--theme-error);">{geoError}</p>
            {/if}
          {/if}
        {:else if field.type === 'boolean'}
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input
              id={inputId}
              type="checkbox"
              checked={bool(field)}
              onchange={(e) => onChange(field.id, e.currentTarget.checked)}
            />
            <span class="opacity-70">Enabled</span>
          </label>
        {:else if field.type === 'select'}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={selectValue(field)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            {#if !field.required && field.default === undefined}
              <option value="">(none)</option>
            {/if}
            {#each field.options ?? [] as opt (opt.value)}
              <option value={opt.value}>{opt.label}</option>
            {/each}
          </select>
        {:else if field.type === 'images'}
          <ImageField
            id={inputId}
            value={values[field.id]}
            onChange={(images) =>
              onChange(field.id, Object.keys(images).length > 0 ? images : undefined)}
            onAttach={referenceImage}
            onDetach={dereferenceImage}
          />
        {:else if field.type === 'ports'}
          {@const ports = (Array.isArray(values[field.id]) ? values[field.id] : []) as ScriptPort[]}
          <div class="flex flex-col gap-1">
            {#each ports as port, i (i)}
              <div class="flex gap-1 items-center">
                <input
                  class="input text-xs flex-1 min-w-0"
                  placeholder="id"
                  value={port.id ?? ''}
                  oninput={(e) => setPort(field.id, i, { id: e.currentTarget.value })}
                />
                <input
                  class="input text-xs flex-1 min-w-0"
                  placeholder="label"
                  value={port.label ?? ''}
                  oninput={(e) => setPort(field.id, i, { label: e.currentTarget.value })}
                />
                <select
                  class="input text-xs"
                  style="width: 7rem;"
                  value={port.type ?? ('any')}
                  onchange={(e) => setPort(field.id, i, { type: e.currentTarget.value })}
                >
                  {#each scriptPortTypes() as t}
                    <option value={t}>{portTypeLabel(t)}</option>
                  {/each}
                </select>
                <button
                  type="button"
                  class="btn btn-danger text-xs px-2 py-1"
                  aria-label="Remove port"
                  onclick={() => removePort(field.id, i)}
                >
                  <i class="las la-times"></i>
                </button>
              </div>
            {/each}
            <button
              type="button"
              class="btn btn-secondary text-xs px-2 py-1 self-start"
              onclick={() => addPort(field.id, 'any')}
            >
              <i class="las la-plus mr-1"></i>Add port
            </button>
          </div>
        {:else if field.type === 'tool_args'}
          {@const args = toolArgList(field.id)}
          <div class="flex flex-col gap-1">
            {#each args as arg, i (i)}
              <div class="flex flex-col gap-1 tool-arg">
                <div class="flex gap-1 items-center">
                  <input
                    class="input text-xs flex-1 min-w-0"
                    placeholder="name"
                    value={arg.id ?? ''}
                    oninput={(e) => setToolArg(field.id, i, { id: e.currentTarget.value })}
                  />
                  <select
                    class="input text-xs"
                    style="width: 7rem;"
                    value={arg.type ?? 'string'}
                    onchange={(e) => setToolArg(field.id, i, { type: e.currentTarget.value })}
                  >
                    {#each toolArgTypes() as t}
                      <option value={t}>{portTypeLabel(t)}</option>
                    {/each}
                  </select>
                  <label class="flex items-center gap-1 text-xs whitespace-nowrap">
                    <input
                      type="checkbox"
                      checked={arg.required ?? false}
                      onchange={(e) => setToolArg(field.id, i, { required: e.currentTarget.checked })}
                    />
                    required
                  </label>
                  <button
                    type="button"
                    class="btn btn-danger text-xs px-2 py-1"
                    aria-label="Remove argument"
                    onclick={() => removeToolArg(field.id, i)}
                  >
                    <i class="las la-times"></i>
                  </button>
                </div>
                <input
                  class="input text-xs"
                  placeholder="what the model should put here"
                  value={arg.description ?? ''}
                  oninput={(e) => setToolArg(field.id, i, { description: e.currentTarget.value })}
                />
              </div>
            {/each}
            <button
              type="button"
              class="btn btn-secondary text-xs px-2 py-1 self-start"
              onclick={() => addToolArg(field.id)}
            >
              <i class="las la-plus mr-1"></i>Add argument
            </button>
          </div>
        {:else if field.type === 'rules'}
          {@const rules = rulesOf<SwitchRule>(field.id)}
          <div class="rule-list">
            {#each rules as rule, i (rule.id ?? i)}
              <div class="rule">
                <div class="rule-head">
                  <span class="rule-port" title="Output {i + 1}: {ruleLabel(rule)}">{i + 1}</span>
                  <select
                    class="input rule-op"
                    value={rule.operator}
                    onchange={(e) =>
                      updateRule<SwitchRule>(field.id, i, { operator: e.currentTarget.value })}
                    aria-label="Rule {i + 1} operator"
                  >
                    {#each field.options ?? [] as opt (opt.value)}
                      <option value={opt.value}>{opt.label}</option>
                    {/each}
                  </select>
                  <button
                    type="button"
                    class="rule-btn"
                    title="Move up"
                    aria-label="Move rule {i + 1} up"
                    disabled={i === 0}
                    onclick={() => moveRule(field.id, i, -1)}
                  >
                    <i class="las la-arrow-up"></i>
                  </button>
                  <button
                    type="button"
                    class="rule-btn"
                    title="Move down"
                    aria-label="Move rule {i + 1} down"
                    disabled={i === rules.length - 1}
                    onclick={() => moveRule(field.id, i, 1)}
                  >
                    <i class="las la-arrow-down"></i>
                  </button>
                  <button
                    type="button"
                    class="rule-btn rule-remove"
                    title="Delete rule (removes its output and any wire on it)"
                    aria-label="Delete rule {i + 1}"
                    onclick={() => removeRule(field.id, i)}
                  >
                    <i class="las la-trash"></i>
                  </button>
                </div>
                {#if operatorNeedsValue(rule.operator)}
                  <input
                    class="input w-full text-sm"
                    class:font-mono={rule.operator === 'expression' || rule.operator === 'regex'}
                    placeholder={rulePlaceholder(rule.operator)}
                    value={rule.value ?? ''}
                    aria-label="Rule {i + 1} value"
                    oninput={(e) =>
                      updateRule<SwitchRule>(field.id, i, { value: e.currentTarget.value })}
                  />
                {/if}
              </div>
            {/each}
            {#if rules.length === 0}
              <p class="text-xs opacity-50">No rules yet — this switch has no outputs.</p>
            {/if}
            <button
              type="button"
              id={inputId}
              class="btn btn-secondary btn-sm self-start"
              onclick={() => addRule(field.id, { id: newRuleId(), operator: 'eq', value: '' })}
            >
              <i class="las la-plus"></i> Add rule
            </button>
          </div>
        {:else if field.type === 'change_rules'}
          {@const rules = rulesOf<ChangeRule>(field.id)}
          <div class="rule-list">
            {#each rules as rule, i (i)}
              {@const op = changeRuleOp(rule)}
              {@const from = changeRuleFrom(rule)}
              <div class="rule">
                <div class="rule-head">
                  <span class="rule-port" title="Rule {i + 1}">{i + 1}</span>
                  <select
                    class="input rule-op"
                    value={op}
                    onchange={(e) =>
                      updateRule<ChangeRule>(field.id, i, changeRuleOpPatch(rule, e.currentTarget.value))}
                    aria-label="Rule {i + 1} operation"
                  >
                    {#each field.options ?? [] as opt (opt.value)}
                      <option value={opt.value}>{opt.label}</option>
                    {/each}
                  </select>
                  <button
                    type="button"
                    class="rule-btn"
                    title="Move up"
                    aria-label="Move rule {i + 1} up"
                    disabled={i === 0}
                    onclick={() => moveRule(field.id, i, -1)}
                  >
                    <i class="las la-arrow-up"></i>
                  </button>
                  <button
                    type="button"
                    class="rule-btn"
                    title="Move down"
                    aria-label="Move rule {i + 1} down"
                    disabled={i === rules.length - 1}
                    onclick={() => moveRule(field.id, i, 1)}
                  >
                    <i class="las la-arrow-down"></i>
                  </button>
                  <button
                    type="button"
                    class="rule-btn rule-remove"
                    title="Delete rule"
                    aria-label="Delete rule {i + 1}"
                    onclick={() => removeRule(field.id, i)}
                  >
                    <i class="las la-trash"></i>
                  </button>
                </div>

                <!-- Move and copy read their source out of the same value
                     field a set writes its literal into, so it comes first to
                     read as "from X to Y". -->
                {#if op === 'move' || op === 'copy'}
                  <label class="rule-field">
                    <span class="rule-label">From property</span>
                    <input
                      class="input w-full text-sm font-mono"
                      placeholder="payload.source"
                      value={changeValueString(rule.value)}
                      oninput={(e) =>
                        updateRule<ChangeRule>(field.id, i, { value: e.currentTarget.value })}
                    />
                  </label>
                {/if}

                <label class="rule-field">
                  <span class="rule-label">{changeTargetLabel(op)}</span>
                  <input
                    class="input w-full text-sm font-mono"
                    placeholder="payload.value"
                    value={rule.target ?? ''}
                    oninput={(e) =>
                      updateRule<ChangeRule>(field.id, i, { target: e.currentTarget.value })}
                  />
                </label>

                {#if op === 'set'}
                  <label class="rule-field">
                    <span class="rule-label">To</span>
                    <span class="rule-value">
                      <select
                        class="input rule-from"
                        value={from}
                        onchange={(e) =>
                          updateRule<ChangeRule>(field.id, i, { from: e.currentTarget.value })}
                        aria-label="Rule {i + 1} value type"
                      >
                        {#each changeValueSources as src (src.value)}
                          <option value={src.value}>{src.label}</option>
                        {/each}
                      </select>
                      <input
                        class="input flex-1 text-sm"
                        class:font-mono={changeValueIsCode(from)}
                        placeholder={changeValuePlaceholder(from)}
                        value={changeValueString(rule.value)}
                        aria-label="Rule {i + 1} value"
                        oninput={(e) =>
                          updateRule<ChangeRule>(field.id, i, { value: e.currentTarget.value })}
                      />
                    </span>
                  </label>
                {:else if op === 'change'}
                  <label class="rule-field">
                    <span class="rule-label">Search for</span>
                    <span class="rule-value">
                      <select
                        class="input rule-from"
                        value={changeRuleSearchType(rule)}
                        onchange={(e) =>
                          updateRule<ChangeRule>(field.id, i, {
                            search_type: e.currentTarget.value,
                          })}
                        aria-label="Rule {i + 1} search type"
                      >
                        {#each changeSearchTypes as t (t.value)}
                          <option value={t.value}>{t.label}</option>
                        {/each}
                      </select>
                      <input
                        class="input flex-1 text-sm"
                        class:font-mono={changeRuleSearchType(rule) === 'regex'}
                        placeholder={changeRuleSearchType(rule) === 'regex' ? '^light\\.' : 'text'}
                        value={rule.search ?? ''}
                        aria-label="Rule {i + 1} search"
                        oninput={(e) =>
                          updateRule<ChangeRule>(field.id, i, { search: e.currentTarget.value })}
                      />
                    </span>
                  </label>
                  <label class="rule-field">
                    <span class="rule-label">Replace with</span>
                    <span class="rule-value">
                      <select
                        class="input rule-from"
                        value={from}
                        onchange={(e) =>
                          updateRule<ChangeRule>(field.id, i, { from: e.currentTarget.value })}
                        aria-label="Rule {i + 1} replacement type"
                      >
                        {#each changeValueSources as src (src.value)}
                          <option value={src.value}>{src.label}</option>
                        {/each}
                      </select>
                      <input
                        class="input flex-1 text-sm"
                        class:font-mono={changeValueIsCode(from)}
                        placeholder={changeRuleSearchType(rule) === 'regex' ? '$1' : 'replacement'}
                        value={rule.replace ?? ''}
                        aria-label="Rule {i + 1} replacement"
                        oninput={(e) =>
                          updateRule<ChangeRule>(field.id, i, { replace: e.currentTarget.value })}
                      />
                    </span>
                  </label>
                  <label class="rule-check">
                    <input
                      type="checkbox"
                      checked={rule.replace_all === true}
                      onchange={(e) =>
                        updateRule<ChangeRule>(field.id, i, {
                          replace_all: e.currentTarget.checked,
                        })}
                    />
                    <span>Replace every match</span>
                  </label>
                  <p class="rule-note">
                    A text property has its matches rewritten; any other property is swapped whole
                    when it matches exactly.
                  </p>
                {/if}
              </div>
            {/each}
            {#if rules.length === 0}
              <p class="text-xs opacity-50">No rules yet — this node passes messages through.</p>
            {/if}
            <button
              type="button"
              id={inputId}
              class="btn btn-secondary btn-sm self-start"
              onclick={() => addRule(field.id, newChangeRule())}
            >
              <i class="las la-plus"></i> Add rule
            </button>
          </div>
        {:else if field.type === 'connection'}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={str(field.id)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            <option value="">(select a connection)</option>
            {#each connectionOptions(field) as conn (conn.id)}
              <option value={conn.id}>{conn.name}</option>
            {/each}
          </select>
          {#if $connectionsStore.loaded && connectionOptions(field).length === 0}
            <p class="text-xs mt-1 opacity-50">
              No matching connections configured. Add one in Settings → Connections.
            </p>
          {/if}
        {:else if editor === 'openhab-item'}
          {@const ohConnId = openhabConnectionId()}
          <div class="entity-field">
            <input
              id={inputId}
              class="input w-full text-sm font-mono"
              placeholder={field.placeholder || ''}
              value={str(field.id)}
              disabled={!ohConnId}
              autocomplete="off"
              oninput={(e) => {
                setString(field.id, e.currentTarget.value);
                openhabOpen = field.id;
              }}
              onfocus={() => {
                openhabOpen = field.id;
                loadOpenHABItems(ohConnId);
              }}
              onblur={() => {
                setTimeout(() => {
                  if (openhabOpen === field.id) openhabOpen = null;
                }, 150);
              }}
            />
            {#if !ohConnId}
              <p class="text-xs mt-1 opacity-50">Select a connection first to browse items.</p>
            {:else if openhabOpen === field.id}
              {@const options = filteredOpenHABItems(field.id)}
              {#if options.length > 0}
                <ul class="entity-dropdown">
                  {#each options as opt, i (opt.name)}
                    {#if i === 0 || openhabGroup(options[i - 1]) !== openhabGroup(opt)}
                      <li class="entity-group">{openhabGroup(opt)}</li>
                    {/if}
                    <li>
                      <button
                        type="button"
                        class="entity-option"
                        onmousedown={(e) => {
                          e.preventDefault();
                          pickEntity(field.id, opt.name);
                          openhabOpen = null;
                        }}
                      >
                        <span class="font-mono">{opt.name}</span>
                        <span class="entity-name">{opt.label}</span>
                        <span class="entity-state">{opt.state}</span>
                      </button>
                    </li>
                  {/each}
                </ul>
              {:else if openhabError}
                <p class="text-xs mt-1" style="color: var(--theme-error);">{openhabError}</p>
              {/if}
            {/if}
          </div>
        {:else if editor === 'ai-model'}
          {@const aiConnId = aiConnectionId()}
          <div class="entity-field">
            <input
              id={inputId}
              class="input w-full text-sm font-mono"
              placeholder={field.placeholder || ''}
              value={str(field.id)}
              disabled={!aiConnId}
              autocomplete="off"
              oninput={(e) => {
                setString(field.id, e.currentTarget.value);
                aiModelOpen = field.id;
              }}
              onfocus={() => {
                aiModelOpen = field.id;
                loadAIModels(aiConnId);
              }}
              onblur={() => {
                // Delay so option mousedown wins over the close.
                setTimeout(() => {
                  if (aiModelOpen === field.id) aiModelOpen = null;
                }, 150);
              }}
            />
            {#if !aiConnId}
              <p class="text-xs mt-1 opacity-50">Select a connection first to browse models.</p>
            {:else if aiModelOpen === field.id}
              {@const options = filteredAIModels(field.id)}
              {#if options.length > 0}
                <ul class="entity-dropdown">
                  {#each options as model (model)}
                    <li>
                      <button
                        type="button"
                        class="entity-option"
                        onmousedown={(e) => {
                          e.preventDefault();
                          setString(field.id, model);
                          aiModelOpen = null;
                        }}
                      >
                        <span class="font-mono">{model}</span>
                      </button>
                    </li>
                  {/each}
                </ul>
              {:else if aiModelError}
                <p class="text-xs mt-1" style="color: var(--theme-error);">{aiModelError}</p>
              {/if}
            {/if}
          </div>
        {:else if field.type === 'entity'}
          {@const entityConnId = entityConnectionId()}
          <div class="entity-field">
            <input
              id={inputId}
              class="input w-full text-sm font-mono"
              placeholder={field.placeholder || ''}
              value={str(field.id)}
              disabled={!entityConnId}
              autocomplete="off"
              oninput={(e) => {
                setString(field.id, e.currentTarget.value);
                entityOpen = field.id;
              }}
              onfocus={() => {
                entityOpen = field.id;
                loadEntities(entityConnId);
              }}
              onblur={() => {
                // Delay so option mousedown wins over the close.
                setTimeout(() => {
                  if (entityOpen === field.id) entityOpen = null;
                }, 150);
              }}
            />
            {#if !entityConnId}
              <p class="text-xs mt-1 opacity-50">Select a connection first to browse entities.</p>
            {:else if entityOpen === field.id}
              {@const options = filteredEntities(field.id)}
              {#if options.length > 0}
                <ul class="entity-dropdown">
                  {#each options as opt, i (opt.entity_id)}
                    {#if i === 0 || entityGroup(options[i - 1]) !== entityGroup(opt)}
                      <li class="entity-group">{entityGroup(opt)}</li>
                    {/if}
                    <li>
                      <button
                        type="button"
                        class="entity-option"
                        onmousedown={(e) => {
                          e.preventDefault();
                          pickEntity(field.id, opt.entity_id);
                        }}
                      >
                        <span class="font-mono">{opt.entity_id}</span>
                        {#if opt.friendly_name}
                          <span class="entity-name">{opt.friendly_name}</span>
                        {/if}
                        {#if opt.device}
                          <span class="entity-name">{opt.device}</span>
                        {/if}
                        <span class="entity-state">{opt.state}</span>
                      </button>
                    </li>
                  {/each}
                </ul>
              {:else if entityError}
                <p class="text-xs mt-1" style="color: var(--theme-error);">{entityError}</p>
              {/if}
            {/if}
          </div>
        {:else if field.type === 'ha-target'}
          {@const targetConnId = entityConnectionId()}
          {@const control = (field.control || 'ha-area').replace(/^ha-/, '')}
          <div class="entity-field">
            <input
              id={inputId}
              class="input w-full text-sm font-mono"
              placeholder={field.placeholder || ''}
              value={str(field.id)}
              disabled={!targetConnId}
              autocomplete="off"
              oninput={(e) => {
                setString(field.id, e.currentTarget.value);
                targetOpen = field.id;
              }}
              onfocus={() => {
                targetOpen = field.id;
                loadRegistry(targetConnId);
              }}
              onblur={() => {
                setTimeout(() => {
                  if (targetOpen === field.id) targetOpen = null;
                }, 150);
              }}
            />
            {#if !targetConnId}
              <p class="text-xs mt-1 opacity-50">Select a connection first to browse {control}s.</p>
            {:else}
              {#if str(field.id) && registryCache[targetConnId]}
                <p class="text-xs mt-1 opacity-60">{targetNames(field.id, control)}</p>
              {/if}
              {#if targetOpen === field.id}
                {@const options = filteredTargets(field.id, control)}
                {#if options.length > 0}
                  <ul class="entity-dropdown">
                    {#each options as opt (opt.id)}
                      <li>
                        <button
                          type="button"
                          class="entity-option"
                          onmousedown={(e) => {
                            e.preventDefault();
                            pickEntity(field.id, opt.id);
                          }}
                        >
                          <span>{opt.name}</span>
                          {#if opt.detail}
                            <span class="entity-name">{opt.detail}</span>
                          {/if}
                          <span class="entity-state font-mono">{opt.id}</span>
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if targetError}
                  <p class="text-xs mt-1" style="color: var(--theme-error);">{targetError}</p>
                {/if}
              {/if}
            {/if}
          </div>
        {:else if field.type === 'expression'}
          <input
            id={inputId}
            class="input w-full text-sm font-mono"
            placeholder={field.placeholder || ''}
            value={str(field.id)}
            oninput={(e) => setString(field.id, e.currentTarget.value)}
          />
        {:else if field.type === 'timezone'}
          <select
            id={inputId}
            class="input w-full text-sm"
            value={str(field.id)}
            onchange={(e) => setString(field.id, e.currentTarget.value)}
          >
            <option value="">Server local time</option>
            {#each timeZones as zone (zone)}
              <option value={zone}>{zone}</option>
            {/each}
          </select>
        {:else if field.type === 'cron'}
          {@const preview = cronPreview[field.id]}
          <input
            id={inputId}
            class="input w-full text-sm font-mono"
            placeholder={field.placeholder || ''}
            value={str(field.id)}
            oninput={(e) => setString(field.id, e.currentTarget.value)}
          />

          <!-- Positional legend: what each position of the expression means. -->
          {#if preview?.fields}
            <div class="cron-legend">
              {#each preview.fields as name (name)}
                <span class="cron-name">{name}</span>
              {/each}
            </div>
          {/if}

          {#if preview}
            {#if preview.valid}
              <p class="cron-description">{preview.description}</p>
              {#if preview.next && preview.next.length > 0}
                <p class="cron-next-label">Next runs</p>
                <ul class="cron-next">
                  {#each preview.next as at, i (at)}
                    <li class:cron-next-first={i === 0}>{formatRunTime(at)}</li>
                  {/each}
                </ul>
              {/if}
            {:else if preview.error}
              <p class="cron-error">{preview.error}</p>
            {/if}
          {/if}
        {:else}
          <!-- string and duration: plain string input. -->
          <input
            id={inputId}
            class="input w-full text-sm"
            placeholder={field.placeholder || ''}
            value={str(field.id)}
            oninput={(e) => setString(field.id, e.currentTarget.value)}
          />
        {/if}
        </fieldset>

        {#if field.help && !helpAbove}
          <p class="text-xs mt-1 opacity-50">{field.help}</p>
        {/if}
      </div>
    {/if}
  {/each}
  {#if spec.length === 0}
    <p class="text-xs opacity-50">This node has no configuration.</p>
  {/if}
</div>

<style>
  .param-toggle {
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
    padding: 0.05rem 0.35rem;
    border-radius: 3px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: none;
    font-size: 0.6rem;
    font-weight: 600;
    color: var(--theme-subtext0, #a6adc8);
    cursor: pointer;
    white-space: nowrap;
  }

  .param-toggle:hover {
    border-color: var(--theme-mauve, #cba6f7);
    color: var(--theme-text, #cdd6f4);
  }

  .param-toggle.on {
    color: var(--theme-mauve, #cba6f7);
    border-color: var(--theme-mauve, #cba6f7);
    background-color: color-mix(in srgb, var(--theme-mauve, #cba6f7) 14%, transparent);
  }

  /* Fills the coordinate pair from the browser, so a sun schedule does not
     need the operator to look their latitude up. */
  .geo-detect {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    margin-top: 0.35rem;
    padding: 0.15rem 0.5rem;
    border-radius: 3px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: none;
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--theme-subtext0, #a6adc8);
    cursor: pointer;
  }

  .geo-detect:hover:not(:disabled) {
    border-color: var(--theme-mauve, #cba6f7);
    color: var(--theme-text, #cdd6f4);
  }

  .geo-detect:disabled {
    cursor: default;
    opacity: 0.6;
  }

  /* Tokens: the picked values as solid chips, the rest of the vocabulary as
     ghost chips one click away; a field with no vocabulary grows a small
     inline input that commits on Enter or comma. */
  .token-box {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem;
    padding: 0.3rem;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 4px;
    background: var(--theme-mantle, #181825);
  }

  .token {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.1rem 0.45rem;
    border-radius: 3px;
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    background: color-mix(in srgb, var(--theme-mauve, #cba6f7) 18%, transparent);
    border: 1px solid color-mix(in srgb, var(--theme-mauve, #cba6f7) 45%, transparent);
    white-space: nowrap;
  }

  .token-x {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border: 0;
    background: none;
    color: var(--theme-subtext0, #a6adc8);
    cursor: pointer;
    font-size: 0.75rem;
    line-height: 1;
  }

  .token-x:hover {
    color: var(--theme-error, #f38ba8);
  }

  .token-add {
    background: none;
    border: 1px dashed var(--theme-surface1, #45475a);
    color: var(--theme-subtext0, #a6adc8);
    font-weight: 500;
    cursor: pointer;
  }

  .token-add:hover {
    border-color: var(--theme-mauve, #cba6f7);
    color: var(--theme-text, #cdd6f4);
  }

  .token-add i {
    font-size: 0.65rem;
  }

  .token-input {
    flex: 1;
    min-width: 6rem;
    border: 0;
    background: none;
    outline: none;
    font-size: 0.75rem;
    color: var(--theme-text, #cdd6f4);
    padding: 0.1rem 0.2rem;
  }

  .token-input::placeholder {
    color: var(--theme-subtext0, #a6adc8);
    opacity: 0.5;
  }

  /* The fieldset exists to disable a wired field's editor in one place; it
     must otherwise be invisible in the layout. */
  .field-editor {
    border: 0;
    margin: 0;
    padding: 0;
    min-inline-size: 0;
  }

  .field-editor:disabled {
    opacity: 0.55;
    pointer-events: none;
  }

  /* Routing rules: one row per rule, numbered to match the output ports on
     the canvas from top to bottom. */
  .rule-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .rule {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 6px;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-base, #1e1e2e);
  }

  .rule-head {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .rule-port {
    flex-shrink: 0;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background-color: var(--theme-surface1, #45475a);
    color: var(--theme-text-muted, #a6adc8);
    font-size: 10px;
    line-height: 18px;
    text-align: center;
  }

  .rule-op {
    flex: 1 1 auto;
    min-width: 0;
    padding: 2px 4px;
    font-size: 12px;
  }

  .rule-btn {
    flex-shrink: 0;
    padding: 2px 4px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text-muted, #a6adc8);
    font-size: 14px;
    line-height: 1;
  }

  .rule-btn:hover:not(:disabled) {
    background-color: var(--theme-surface1, #45475a);
    color: var(--theme-text, #cdd6f4);
  }

  .rule-btn:disabled {
    opacity: 0.25;
    cursor: default;
  }

  .rule-remove:hover:not(:disabled) {
    color: var(--theme-error, #f38ba8);
  }

  /* Change rules carry several operands per row, so each gets a caption and
     the typed ones pair a type select with their input. */
  .rule-field {
    display: block;
  }

  .rule-label {
    display: block;
    margin-bottom: 2px;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    opacity: 0.55;
  }

  .rule-value {
    display: flex;
    gap: 4px;
  }

  /* An input's intrinsic width would push the row past the panel, so the
     operand shrinks instead of scrolling the panel sideways. */
  .rule-value > input {
    flex: 1 1 auto;
    min-width: 0;
  }

  .rule-from {
    flex: 0 0 auto;
    width: 88px;
    min-width: 0;
    padding: 2px 4px;
    font-size: 12px;
  }

  .rule-check {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    opacity: 0.75;
    cursor: pointer;
  }

  .rule-note {
    font-size: 10px;
    line-height: 1.4;
    opacity: 0.45;
  }

  .entity-field {
    position: relative;
  }

  .entity-dropdown {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 30;
    max-height: 220px;
    overflow-y: auto;
    margin-top: 2px;
    padding: 2px;
    list-style: none;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-mantle, #181825);
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.35);
  }

  .entity-group {
    padding: 0.35rem 0.5rem 0.15rem;
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    opacity: 0.6;
  }

  .entity-option {
    display: flex;
    align-items: baseline;
    gap: 8px;
    width: 100%;
    padding: 4px 8px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }

  .entity-option:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .entity-name {
    opacity: 0.6;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .entity-state {
    margin-left: auto;
    opacity: 0.4;
    flex-shrink: 0;
  }
  /* Cron editor: a positional legend plus a live reading. */
  .cron-legend {
    display: flex;
    gap: 4px;
    margin-top: 3px;
    padding: 0 2px;
  }

  .cron-name {
    flex: 1 1 0;
    min-width: 0;
    font-size: 9px;
    line-height: 1.15;
    text-align: center;
    opacity: 0.55;
  }

  .cron-description {
    margin-top: 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--theme-success, #a6e3a1);
  }

  .cron-error {
    margin-top: 6px;
    font-size: 11px;
    color: var(--theme-error, #f38ba8);
  }

  .cron-next-label {
    margin-top: 6px;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    opacity: 0.5;
  }

  .cron-next {
    margin-top: 2px;
    list-style: none;
    font-size: 11px;
    opacity: 0.7;
  }

  .cron-next-first {
    opacity: 1;
    font-weight: 600;
  }
  .node-pick {
    background: var(--theme-surface0, #313244);
  }

  .node-pick:hover {
    background: var(--theme-surface1, #45475a);
  }
</style>
