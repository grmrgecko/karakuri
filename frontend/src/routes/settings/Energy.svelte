<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { navigate } from 'svelte-routing';
  import { api } from '../../lib/api/client';
  import { confirm } from '../../lib/stores/confirmDialog';
  import { hasPermission } from '../../lib/stores/auth';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';

  interface Source {
    id: string;
    name: string;
    source_kind: 'control' | 'device' | 'ha';
    source_id: string;
    entity_id?: string;
    attribute?: string;
    measurement: 'energy' | 'power';
    unit: string;
    role: string;
    gap_limit_s: number;
    max_kw: number;
    last_value: number | null;
    last_at: string | null;
    state: string;
    state_note?: string;
    today_kwh: number;
    group_ids: string[];
    import?: { running: boolean; days: number; done: number; hours: number; error?: string };
  }
  interface Candidate {
    name: string;
    source_kind: Source['source_kind'];
    source_id: string;
    entity_id?: string;
    attribute?: string;
    measurement: Source['measurement'];
    unit: string;
    value?: number;
    source_name: string;
    added: boolean;
  }
  interface Group {
    id: string;
    name: string;
    sort_order: number;
    source_ids: string[];
  }
  interface Period {
    name: string;
    days: number[];
    start: string;
    end: string;
    rate: number;
  }
  interface RateSpec {
    flat?: number;
    periods?: Period[];
    control_id?: string;
  }
  interface Tariff {
    id: string;
    name: string;
    currency: string;
    effective_from: string;
    import: RateSpec;
    export: RateSpec | null;
    daily_charge: number;
  }

  const ROLES: Record<string, string> = {
    consumer: 'Consumer',
    house: 'Whole house',
    import: 'Grid import',
    export: 'Grid export',
    production: 'Solar production',
  };
  const DAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

  let canWrite = $derived(hasPermission('api:connections:write'));
  let canDashboards = $derived(hasPermission('api:dashboards:write'));

  let loading = $state(true);
  let error = $state('');
  let sources = $state<Source[]>([]);
  let groups = $state<Group[]>([]);
  let tariffs = $state<Tariff[]>([]);
  let settings = $state({ timezone: '', week_start: 1 });
  let settingsSaved = $state('');
  let presetBusy = $state(false);
  let refreshTimer: ReturnType<typeof setInterval> | null = null;

  onMount(async () => {
    await refresh();
    loading = false;
    // States and today's usage move as readings arrive.
    refreshTimer = setInterval(() => {
      if (!sourceModal && !groupModal && !tariffModal) void refreshSources();
    }, 15000);
  });
  onDestroy(() => {
    if (refreshTimer) clearInterval(refreshTimer);
  });

  async function refresh(): Promise<void> {
    try {
      const [s, g, t, st] = await Promise.all([
        api.get<Source[]>('/energy/sources'),
        api.get<Group[]>('/energy/groups'),
        api.get<Tariff[]>('/energy/tariffs'),
        api.get<{ timezone: string; week_start: number }>('/energy/settings'),
      ]);
      sources = s ?? [];
      groups = g ?? [];
      tariffs = t ?? [];
      settings = { timezone: st?.timezone ?? '', week_start: st?.week_start || 1 };
      error = '';
    } catch (e: any) {
      error = e.message || 'Energy settings could not be read';
    }
  }

  async function refreshSources(): Promise<void> {
    try {
      sources = (await api.get<Source[]>('/energy/sources')) ?? [];
    } catch {
      // The next refresh tries again.
    }
  }

  async function saveSettings(): Promise<void> {
    settingsSaved = '';
    try {
      await api.put('/energy/settings', settings);
      settingsSaved = 'Saved';
    } catch (e: any) {
      settingsSaved = e.message || 'Not saved';
    }
  }

  async function createPreset(): Promise<void> {
    presetBusy = true;
    try {
      const d = await api.post<{ id: string; slug: string }>('/energy/preset');
      navigate(`/dashboard/${d.id}`);
    } catch (e: any) {
      error = e.message || 'The dashboard could not be created';
    } finally {
      presetBusy = false;
    }
  }

  function reads(s: Source): string {
    if (s.source_kind === 'ha') return `${s.entity_id}${s.attribute ? ' · ' + s.attribute : ''}`;
    return `${s.source_kind === 'device' ? 'Device' : 'Control'}${s.attribute ? ' · ' + s.attribute : ''}`;
  }

  function lastReading(s: Source): string {
    if (s.last_value === null || s.last_value === undefined) return 'no reading yet';
    const when = s.last_at ? new Date(s.last_at).toLocaleString() : '';
    return `${Number(s.last_value).toLocaleString(undefined, { maximumFractionDigits: 3 })} ${s.unit} · ${when}`;
  }

  // ---- Sources ----

  let sourceModal = $state(false);
  let editingSource = $state<Source | null>(null);
  let sourceForm = $state(emptySource());
  let sourceError = $state('');
  let candidates = $state<Candidate[]>([]);
  let candidatesLoading = $state(false);
  let showCandidates = $state(false);

  function emptySource() {
    return {
      name: '',
      source_kind: 'device' as Source['source_kind'],
      source_id: '',
      entity_id: '',
      attribute: '',
      measurement: 'energy' as Source['measurement'],
      unit: 'kWh',
      role: 'consumer',
      gap_limit_s: 0,
      max_kw: 0,
    };
  }

  function openSource(s: Source | null): void {
    editingSource = s;
    sourceForm = s
      ? {
          name: s.name,
          source_kind: s.source_kind,
          source_id: s.source_id,
          entity_id: s.entity_id ?? '',
          attribute: s.attribute ?? '',
          measurement: s.measurement,
          unit: s.unit,
          role: s.role,
          gap_limit_s: s.gap_limit_s,
          max_kw: s.max_kw,
        }
      : emptySource();
    sourceError = '';
    sourceModal = true;
  }

  async function loadCandidates(): Promise<void> {
    showCandidates = true;
    candidatesLoading = true;
    try {
      candidates = (await api.get<Candidate[]>('/energy/candidates')) ?? [];
    } catch (e: any) {
      error = e.message || 'Suggestions could not be read';
    } finally {
      candidatesLoading = false;
    }
  }

  function useCandidate(c: Candidate): void {
    showCandidates = false;
    openSource(null);
    sourceForm = {
      ...sourceForm,
      name: c.name,
      source_kind: c.source_kind,
      source_id: c.source_id,
      entity_id: c.entity_id ?? '',
      attribute: c.attribute ?? '',
      measurement: c.measurement,
      unit: c.unit,
    };
  }

  async function saveSource(): Promise<void> {
    sourceError = '';
    try {
      if (editingSource) await api.put(`/energy/sources/${editingSource.id}`, sourceForm);
      else await api.post('/energy/sources', sourceForm);
      sourceModal = false;
      await refresh();
    } catch (e: any) {
      sourceError = e.message || 'The source could not be saved';
    }
  }

  async function deleteSource(s: Source): Promise<void> {
    if (!(await confirm(`Delete ${s.name}? Its recorded usage is removed after 30 days.`))) return;
    try {
      await api.delete(`/energy/sources/${s.id}`);
      await refresh();
    } catch (e: any) {
      error = e.message || 'The source could not be deleted';
    }
  }

  async function importHistory(s: Source): Promise<void> {
    const days = Number(prompt(`Import how many days of ${s.name}'s history? (1 to 400)`, '30'));
    if (!Number.isFinite(days) || days < 1) return;
    try {
      await api.post(`/energy/sources/${s.id}/import`, { days: Math.min(400, Math.round(days)) });
      await refreshSources();
    } catch (e: any) {
      error = e.message || 'The import could not start';
    }
  }

  // ---- Groups ----

  let groupModal = $state(false);
  let editingGroup = $state<Group | null>(null);
  let groupForm = $state({ name: '', sort_order: 0, source_ids: [] as string[] });
  let groupError = $state('');

  function openGroup(g: Group | null): void {
    editingGroup = g;
    groupForm = g ? { name: g.name, sort_order: g.sort_order, source_ids: [...g.source_ids] } : { name: '', sort_order: groups.length, source_ids: [] };
    groupError = '';
    groupModal = true;
  }

  async function saveGroup(): Promise<void> {
    groupError = '';
    try {
      if (editingGroup) await api.put(`/energy/groups/${editingGroup.id}`, groupForm);
      else await api.post('/energy/groups', groupForm);
      groupModal = false;
      await refresh();
    } catch (e: any) {
      groupError = e.message || 'The group could not be saved';
    }
  }

  async function deleteGroup(g: Group): Promise<void> {
    if (!(await confirm(`Delete ${g.name}? Its sources stay.`))) return;
    try {
      await api.delete(`/energy/groups/${g.id}`);
      await refresh();
    } catch (e: any) {
      error = e.message || 'The group could not be deleted';
    }
  }

  // ---- Tariffs ----

  let tariffModal = $state(false);
  let editingTariff = $state<Tariff | null>(null);
  let tariffError = $state('');
  let tariffResult = $state('');
  let tariffForm = $state(emptyTariff());

  function emptyTariff() {
    return {
      name: '',
      currency: 'USD',
      effective_from: new Date().toISOString().slice(0, 10),
      daily_charge: 0,
      mode: 'flat' as 'flat' | 'periods' | 'control',
      flat: 0.15,
      control_id: '',
      periods: [{ name: 'peak', days: [1, 2, 3, 4, 5], start: '16:00', end: '21:00', rate: 0.3 }] as Period[],
      export: '' as string,
    };
  }

  function openTariff(t: Tariff | null): void {
    editingTariff = t;
    tariffError = '';
    if (!t) {
      tariffForm = emptyTariff();
    } else {
      const imp = t.import ?? {};
      tariffForm = {
        name: t.name,
        currency: t.currency,
        effective_from: t.effective_from,
        daily_charge: t.daily_charge,
        mode: imp.periods?.length ? 'periods' : imp.control_id ? 'control' : 'flat',
        flat: imp.flat ?? 0,
        control_id: imp.control_id ?? '',
        periods: imp.periods?.map((p) => ({ ...p, days: [...p.days] })) ?? [],
        export: t.export?.flat !== undefined ? String(t.export.flat) : '',
      };
    }
    tariffModal = true;
  }

  function addPeriod(): void {
    tariffForm.periods = [...tariffForm.periods, { name: '', days: [1, 2, 3, 4, 5, 6, 7], start: '00:00', end: '00:00', rate: 0 }];
  }

  function toggleDay(p: Period, d: number): void {
    p.days = p.days.includes(d) ? p.days.filter((x) => x !== d) : [...p.days, d].sort();
    tariffForm.periods = [...tariffForm.periods];
  }

  async function saveTariff(): Promise<void> {
    tariffError = '';
    const imp: RateSpec =
      tariffForm.mode === 'periods'
        ? { periods: tariffForm.periods.map((p) => ({ ...p, rate: Number(p.rate) })) }
        : tariffForm.mode === 'control'
          ? { control_id: tariffForm.control_id }
          : { flat: Number(tariffForm.flat) };
    const body = {
      name: tariffForm.name,
      currency: tariffForm.currency,
      effective_from: tariffForm.effective_from,
      daily_charge: Number(tariffForm.daily_charge) || 0,
      import: imp,
      export: tariffForm.export.trim() === '' ? null : { flat: Number(tariffForm.export) },
    };
    try {
      const res = editingTariff
        ? await api.put<{ hours_recomputed: number }>(`/energy/tariffs/${editingTariff.id}`, body)
        : await api.post<{ hours_recomputed: number }>('/energy/tariffs', body);
      tariffModal = false;
      tariffResult = `Saved; ${res?.hours_recomputed ?? 0} recorded hours repriced.`;
      await refresh();
    } catch (e: any) {
      tariffError = e.message || 'The tariff could not be saved';
    }
  }

  async function deleteTariff(t: Tariff): Promise<void> {
    if (!(await confirm(`Delete ${t.name}? Its hours are repriced under whatever covers them now.`))) return;
    try {
      await api.delete(`/energy/tariffs/${t.id}`);
      await refresh();
    } catch (e: any) {
      error = e.message || 'The tariff could not be deleted';
    }
  }

  function rateSummary(t: Tariff): string {
    const imp = t.import ?? {};
    if (imp.periods?.length) return imp.periods.map((p) => `${p.name || 'period'} ${p.rate}`).join(', ');
    if (imp.control_id) return 'from a control';
    return `${imp.flat ?? 0} flat`;
  }
</script>

<h2 class="text-xl font-semibold mb-2">Energy</h2>
<p class="text-sm opacity-70 mb-6 max-w-3xl">
  Energy sources are meters and plugs reporting kWh or watts. Karakuri records their usage per hour,
  prices each hour under the tariff in force, and shows the totals on charts and in flows. A cumulative
  meter's resets and gaps are handled; a source whose history is forwarded, or a Home Assistant entity,
  has missed hours filled from that history.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{error}</div>
{/if}

{#if loading}
  <p class="opacity-60">Loading energy settings</p>
{:else}
  <section class="mb-8">
    <div class="flex items-center justify-between mb-2">
      <h3 class="font-semibold">Sources</h3>
      <div class="flex gap-2">
        {#if canDashboards}
          <button class="btn btn-secondary" onclick={createPreset} disabled={presetBusy || sources.length === 0}>
            <i class="las la-chart-bar mr-1"></i>{presetBusy ? 'Creating...' : 'Create energy dashboard'}
          </button>
        {/if}
        {#if canWrite}
          <button class="btn btn-secondary" onclick={loadCandidates}><i class="las la-search mr-1"></i>Suggest sources</button>
          <button class="btn btn-primary" onclick={() => openSource(null)}><i class="las la-plus mr-1"></i>Add source</button>
        {/if}
      </div>
    </div>
    {#if sources.length === 0}
      <p class="text-sm opacity-60">No sources yet. Suggest sources lists devices, controls, and Home Assistant sensors reporting kWh or watts.</p>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
              <th class="py-2 pr-4 text-left">Name</th>
              <th class="py-2 pr-4 text-left">Role</th>
              <th class="py-2 pr-4 text-left">Reads</th>
              <th class="py-2 pr-4 text-left">State</th>
              <th class="py-2 pr-4 text-right">Today</th>
              <th class="py-2"></th>
            </tr>
          </thead>
          <tbody>
            {#each sources as s (s.id)}
              <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
                <td class="py-2 pr-4 font-medium">{s.name}</td>
                <td class="py-2 pr-4 text-xs">{ROLES[s.role] ?? s.role}</td>
                <td class="py-2 pr-4 text-xs opacity-80">
                  {reads(s)}
                  <div class="opacity-70">{s.measurement === 'energy' ? 'Meter' : 'Power'} in {s.unit}</div>
                </td>
                <td class="py-2 pr-4 text-xs">
                  <span class="state state-{s.state}">{s.state}</span>
                  <div class="opacity-70">{lastReading(s)}</div>
                  {#if s.state_note}<div class="opacity-70">{s.state_note}</div>{/if}
                  {#if s.import}
                    <div class="opacity-70">
                      {s.import.running ? `Importing: ${s.import.done} of ${s.import.days} days` : `Imported ${s.import.hours} hours`}
                      {#if s.import.error}<span class="text-red-400"> · {s.import.error}</span>{/if}
                    </div>
                  {/if}
                </td>
                <td class="py-2 pr-4 text-right mono">{s.today_kwh.toFixed(2)} kWh</td>
                <td class="py-2 text-right whitespace-nowrap">
                  {#if canWrite}
                    <button class="icon-btn" title="Import history" onclick={() => importHistory(s)}><i class="la la-history"></i></button>
                    <button class="icon-btn" title="Edit" onclick={() => openSource(s)}><i class="la la-pen"></i></button>
                    <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => deleteSource(s)}><i class="la la-trash"></i></button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <section class="mb-8">
    <div class="flex items-center justify-between mb-2">
      <h3 class="font-semibold">Groups</h3>
      {#if canWrite}
        <button class="btn btn-secondary" onclick={() => openGroup(null)} disabled={sources.length === 0}><i class="las la-plus mr-1"></i>Add group</button>
      {/if}
    </div>
    {#if groups.length === 0}
      <p class="text-sm opacity-60">No groups. A group, such as a room or a circuit, sums its sources.</p>
    {:else}
      <ul class="text-sm">
        {#each groups as g (g.id)}
          <li class="flex items-center justify-between py-1 border-b" style="border-color: var(--theme-surface1, #45475a)">
            <span><span class="font-medium">{g.name}</span>
              <span class="opacity-70"> · {g.source_ids.map((id) => sources.find((s) => s.id === id)?.name ?? 'deleted source').join(', ') || 'no sources'}</span></span>
            {#if canWrite}
              <span>
                <button class="icon-btn" title="Edit" onclick={() => openGroup(g)}><i class="la la-pen"></i></button>
                <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => deleteGroup(g)}><i class="la la-trash"></i></button>
              </span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section class="mb-8">
    <div class="flex items-center justify-between mb-2">
      <h3 class="font-semibold">Tariffs</h3>
      {#if canWrite}
        <button class="btn btn-secondary" onclick={() => openTariff(null)}><i class="las la-plus mr-1"></i>Add tariff</button>
      {/if}
    </div>
    {#if tariffResult}<p class="text-sm opacity-80 mb-2">{tariffResult}</p>{/if}
    {#if tariffs.length === 0}
      <p class="text-sm opacity-60">No tariff yet; usage is recorded but has no cost until one covers it.</p>
    {:else}
      <ul class="text-sm">
        {#each tariffs as t (t.id)}
          <li class="flex items-center justify-between py-1 border-b" style="border-color: var(--theme-surface1, #45475a)">
            <span><span class="font-medium">{t.name}</span>
              <span class="opacity-70"> · from {t.effective_from} · {t.currency} · {rateSummary(t)}{t.daily_charge ? ` · ${t.daily_charge} a day` : ''}{t.export?.flat !== undefined ? ` · export ${t.export.flat}` : ''}</span></span>
            {#if canWrite}
              <span>
                <button class="icon-btn" title="Edit" onclick={() => openTariff(t)}><i class="la la-pen"></i></button>
                <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => deleteTariff(t)}><i class="la la-trash"></i></button>
              </span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section class="mb-8">
    <h3 class="font-semibold mb-2">Time</h3>
    <div class="flex gap-3 items-end flex-wrap">
      <FormField label="Time zone" id="energy-tz">
        <input id="energy-tz" class="input" placeholder="The server's zone" bind:value={settings.timezone} disabled={!canWrite} />
      </FormField>
      <FormField label="Week starts on" id="energy-week">
        <select id="energy-week" class="input" bind:value={settings.week_start} disabled={!canWrite}>
          {#each DAYS as d, i}<option value={i + 1}>{d}</option>{/each}
        </select>
      </FormField>
      {#if canWrite}<button class="btn btn-secondary mb-1" onclick={saveSettings}>Save</button>{/if}
      {#if settingsSaved}<span class="text-sm opacity-70 mb-2">{settingsSaved}</span>{/if}
    </div>
  </section>
{/if}

{#if showCandidates}
  <Modal title="Suggested sources" maxWidth="4xl" onclose={() => (showCandidates = false)}>
    {#if candidatesLoading}
      <p class="opacity-60">Looking for energy and power readings</p>
    {:else if candidates.length === 0}
      <p class="text-sm opacity-60">Nothing reports kWh or watts. Add a source by hand with its device, control, or entity.</p>
    {:else}
      <div class="overflow-x-auto max-h-[60vh]">
        <table class="w-full text-sm">
          <tbody>
            {#each candidates as c}
              <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)" class:opacity-60={c.added}>
                <td class="py-2 pr-4"><div class="font-medium">{c.name}</div><div class="text-xs opacity-60">{c.source_name}</div></td>
                <td class="py-2 pr-4 text-xs">{c.measurement === 'energy' ? 'Meter' : 'Power'} · {c.value ?? '?'} {c.unit}</td>
                <td class="py-2 text-right">
                  {#if c.added}<span class="text-xs opacity-70">added</span>
                  {:else}<button class="btn btn-secondary" onclick={() => useCandidate(c)}>Use</button>{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Modal>
{/if}

{#if sourceModal}
  <Modal title={editingSource ? 'Edit source' : 'Add source'} maxWidth="2xl" onclose={() => (sourceModal = false)}>
    <div class="flex flex-col gap-3">
      <FormField label="Name" id="src-name"><input id="src-name" class="input" bind:value={sourceForm.name} /></FormField>
      <div class="grid grid-cols-2 gap-3">
        <FormField label="Reads" id="src-kind">
          <select id="src-kind" class="input" bind:value={sourceForm.source_kind}>
            <option value="device">Device</option>
            <option value="control">Control</option>
            <option value="ha">Home Assistant entity</option>
          </select>
        </FormField>
        <FormField label={sourceForm.source_kind === 'ha' ? 'Connection ID' : `${sourceForm.source_kind === 'device' ? 'Device' : 'Control'} ID`} id="src-id">
          <input id="src-id" class="input mono" bind:value={sourceForm.source_id} />
        </FormField>
        {#if sourceForm.source_kind === 'ha'}
          <FormField label="Entity" id="src-entity"><input id="src-entity" class="input mono" placeholder="sensor.house_energy" bind:value={sourceForm.entity_id} /></FormField>
        {/if}
        <FormField label="Attribute" id="src-attr">
          <input id="src-attr" class="input mono" placeholder={sourceForm.source_kind === 'device' ? 'value' : 'The value'} bind:value={sourceForm.attribute} />
        </FormField>
        <FormField label="Measures" id="src-measure">
          <select id="src-measure" class="input" bind:value={sourceForm.measurement}
                  onchange={() => (sourceForm.unit = sourceForm.measurement === 'energy' ? 'kWh' : 'W')}>
            <option value="energy">Energy, a meter that counts up</option>
            <option value="power">Power</option>
          </select>
        </FormField>
        <FormField label="Unit" id="src-unit">
          <select id="src-unit" class="input" bind:value={sourceForm.unit}>
            {#each sourceForm.measurement === 'energy' ? ['Wh', 'kWh', 'MWh'] : ['W', 'kW', 'MW'] as u}<option value={u}>{u}</option>{/each}
          </select>
        </FormField>
        <FormField label="Role" id="src-role">
          <select id="src-role" class="input" bind:value={sourceForm.role}>
            {#each Object.entries(ROLES) as [id, label]}<option value={id}>{label}</option>{/each}
          </select>
        </FormField>
        <FormField label="Gap limit (seconds)" id="src-gap">
          <input id="src-gap" class="input" type="number" min="0" placeholder={sourceForm.measurement === 'power' ? '900' : '21600'} bind:value={sourceForm.gap_limit_s} />
        </FormField>
        <FormField label="Plausible maximum (kW)" id="src-max">
          <input id="src-max" class="input" type="number" min="0" placeholder="25" bind:value={sourceForm.max_kw} />
        </FormField>
      </div>
      <p class="text-xs opacity-70">
        Readings that rise faster than the plausible maximum are rejected as glitches. A meter silent for
        longer than the gap limit has its usage placed in the hour it reports again, or filled from its
        history when it has one; a silent power sensor adds nothing for the silence.
      </p>
      {#if sourceError}<div class="p-2 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{sourceError}</div>{/if}
      <div class="flex gap-2 justify-end">
        <button class="btn btn-secondary" onclick={() => (sourceModal = false)}>Cancel</button>
        <button class="btn btn-primary" onclick={saveSource}>Save</button>
      </div>
    </div>
  </Modal>
{/if}

{#if groupModal}
  <Modal title={editingGroup ? 'Edit group' : 'Add group'} maxWidth="2xl" onclose={() => (groupModal = false)}>
    <div class="flex flex-col gap-3">
      <FormField label="Name" id="grp-name"><input id="grp-name" class="input" placeholder="Kitchen" bind:value={groupForm.name} /></FormField>
      <div class="flex flex-col gap-1 max-h-[40vh] overflow-y-auto">
        {#each sources as s (s.id)}
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={groupForm.source_ids.includes(s.id)}
                   onchange={(e) => (groupForm.source_ids = e.currentTarget.checked ? [...groupForm.source_ids, s.id] : groupForm.source_ids.filter((x) => x !== s.id))} />
            {s.name}
          </label>
        {/each}
      </div>
      <p class="text-xs opacity-70">A change of members applies from now; past hours keep the grouping they had.</p>
      {#if groupError}<div class="p-2 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{groupError}</div>{/if}
      <div class="flex gap-2 justify-end">
        <button class="btn btn-secondary" onclick={() => (groupModal = false)}>Cancel</button>
        <button class="btn btn-primary" onclick={saveGroup}>Save</button>
      </div>
    </div>
  </Modal>
{/if}

{#if tariffModal}
  <Modal title={editingTariff ? 'Edit tariff' : 'Add tariff'} maxWidth="4xl" onclose={() => (tariffModal = false)}>
    <div class="flex flex-col gap-3">
      <div class="grid grid-cols-2 md:grid-cols-4 gap-3">
        <FormField label="Name" id="tar-name"><input id="tar-name" class="input" bind:value={tariffForm.name} /></FormField>
        <FormField label="Currency" id="tar-cur"><input id="tar-cur" class="input" maxlength="3" bind:value={tariffForm.currency} /></FormField>
        <FormField label="Starts on" id="tar-from"><input id="tar-from" class="input" type="date" bind:value={tariffForm.effective_from} /></FormField>
        <FormField label="Daily charge" id="tar-daily"><input id="tar-daily" class="input" type="number" step="0.01" bind:value={tariffForm.daily_charge} /></FormField>
      </div>
      <FormField label="Rate per kWh" id="tar-mode">
        <select id="tar-mode" class="input" bind:value={tariffForm.mode}>
          <option value="flat">One rate</option>
          <option value="periods">Time-of-use periods</option>
          <option value="control">From a number control a flow sets</option>
        </select>
      </FormField>
      {#if tariffForm.mode === 'flat'}
        <FormField label="Rate" id="tar-flat"><input id="tar-flat" class="input" type="number" step="0.0001" bind:value={tariffForm.flat} /></FormField>
      {:else if tariffForm.mode === 'control'}
        <FormField label="Control ID" id="tar-control"><input id="tar-control" class="input mono" bind:value={tariffForm.control_id} /></FormField>
        <p class="text-xs opacity-70">Each hour is priced at the control's average over the hour. Future prices are unknown, so the cheapest window cannot be found under this tariff.</p>
      {:else}
        <table class="w-full text-sm">
          <thead><tr><th class="text-left">Name</th><th class="text-left">Days</th><th class="text-left">From</th><th class="text-left">To</th><th class="text-left">Rate</th><th></th></tr></thead>
          <tbody>
            {#each tariffForm.periods as p, i}
              <tr>
                <td class="pr-2"><input class="input" bind:value={p.name} aria-label="Period name" /></td>
                <td class="pr-2 whitespace-nowrap">
                  {#each DAYS as d, di}
                    <button type="button" class="day" class:on={p.days.includes(di + 1)} onclick={() => toggleDay(p, di + 1)}>{d[0]}</button>
                  {/each}
                </td>
                <td class="pr-2"><input class="input" type="time" bind:value={p.start} aria-label="Start" /></td>
                <td class="pr-2"><input class="input" type="time" bind:value={p.end} aria-label="End" /></td>
                <td class="pr-2"><input class="input" type="number" step="0.0001" bind:value={p.rate} aria-label="Rate" /></td>
                <td><button class="icon-btn icon-btn-danger" title="Remove" onclick={() => (tariffForm.periods = tariffForm.periods.filter((_, j) => j !== i))}><i class="la la-times"></i></button></td>
              </tr>
            {/each}
          </tbody>
        </table>
        <div><button class="btn btn-secondary" onclick={addPeriod}>Add period</button></div>
        <p class="text-xs opacity-70">Periods must cover every hour of the week once. An end of 00:00 means midnight; split a period that crosses midnight in two.</p>
      {/if}
      <FormField label="Export credit per kWh" id="tar-export">
        <input id="tar-export" class="input" type="number" step="0.0001" placeholder="None" bind:value={tariffForm.export} />
      </FormField>
      {#if tariffError}<div class="p-2 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{tariffError}</div>{/if}
      <div class="flex gap-2 justify-end">
        <button class="btn btn-secondary" onclick={() => (tariffModal = false)}>Cancel</button>
        <button class="btn btn-primary" onclick={saveTariff}>Save</button>
      </div>
    </div>
  </Modal>
{/if}

<style>
  /* Fields fill their grid cell rather than their default width. */
  input.input,
  select.input {
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }
  .mono {
    font-family: var(--theme-font-mono, ui-monospace, monospace);
  }
  .state {
    font-weight: 600;
  }
  .state-ok {
    color: var(--theme-success, #a6e3a1);
  }
  .state-gap,
  .state-reset,
  .state-waiting {
    color: var(--theme-warning, #f9e2af);
  }
  .state-rejected,
  .state-unavailable {
    color: var(--theme-error, #f38ba8);
  }
  .day {
    width: 1.6rem;
    height: 1.6rem;
    margin-right: 2px;
    border-radius: 4px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: transparent;
    color: inherit;
    font-size: 0.75rem;
  }
  .day.on {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
  }
</style>
