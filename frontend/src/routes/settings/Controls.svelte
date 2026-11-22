<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { controlsStore, type Control, type ControlKind } from '../../lib/stores/controls';
  import { websocket } from '../../lib/stores/websocket';
  import { confirm } from '../../lib/stores/confirmDialog';
  import { hasPermission } from '../../lib/stores/auth';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import ConfigForm from '../../lib/components/editor/ConfigForm.svelte';
  import HistoryRecordField from '../../lib/components/HistoryRecordField.svelte';
  // The dashboard widgets' own face, so operating a control here is the same
  // gesture a dashboard row offers: every kind, composite ones included,
  // drawn by the one component that knows how.
  import ControlFace from '../../../../plugins/core/widgets/shared/faces/ControlFace.svelte';
  import { loadControlKinds } from '../../../../plugins/core/widgets/shared/controlKinds';
  import type { Resolved } from '../../../../plugins/core/widgets/shared/entitySource';

  let loading = $state(true);
  let error = $state('');
  let controls = $derived($controlsStore.controls);
  let kinds = $derived($controlsStore.kinds);

  let canWrite = $derived(hasPermission('api:controls:write'));
  // History forwards are connection settings, so recording one from here takes
  // connection write access as well.
  let canRecordHistory = $derived(hasPermission('api:connections:write'));
  let historyField: ReturnType<typeof HistoryRecordField> | undefined = $state();

  // Create/edit modal state. Kind stays editable on an existing control, but
  // changing it re-seeds the value, which the form says out loud.
  let showModal = $state(false);
  let editing: Control | null = $state(null);
  let saving = $state(false);
  let formError = $state('');
  let form = $state({ name: '', kind: '', icon: '', allow_public: false });
  let options = $state<Record<string, unknown>>({});

  let kindDef = $derived(kinds.find((k) => k.name === form.kind));

  let unsubscribeWs: (() => void) | null = null;

  onMount(async () => {
    try {
      await controlsStore.load();
      await controlsStore.refresh();
      // The faces read the kind catalog through their own module, which is
      // fetched once and shared with any dashboard widget on the page.
      await loadControlKinds();
    } catch (e: any) {
      error = e.message || 'Failed to load controls';
    } finally {
      loading = false;
    }

    // Values move from dashboards and flows too, so the panel follows the
    // broker rather than showing whatever was true at page load.
    unsubscribeWs = websocket.subscribeTopic('control:*', (data) => {
      const change = data as { control?: Control; deleted?: boolean; id?: string };
      if (change?.deleted && change.id) {
        controlsStore.applyDeleted(change.id);
        return;
      }
      if (change?.control?.id) {
        controlsStore.applyValue(change.control.id, change.control.value, change.control.updated_at);
      }
    });
  });

  onDestroy(() => {
    unsubscribeWs?.();
  });

  // defaultOptions seeds a fresh control from its kind's declared defaults.
  function defaultOptions(kind: string): Record<string, unknown> {
    const def = kinds.find((k) => k.name === kind);
    const out: Record<string, unknown> = {};
    for (const field of def?.options ?? []) {
      if (field.default !== undefined) out[field.id] = field.default;
    }
    return out;
  }

  function openCreate() {
    editing = null;
    formError = '';
    form = { name: '', kind: kinds[0]?.name ?? '', icon: '', allow_public: false };
    options = defaultOptions(form.kind);
    showModal = true;
  }

  function openEdit(control: Control) {
    editing = control;
    formError = '';
    form = {
      name: control.name,
      kind: control.kind,
      icon: control.icon || '',
      allow_public: control.allow_public,
    };
    options = { ...(control.options || {}) };
    showModal = true;
  }

  function setOption(fieldId: string, value: unknown) {
    if (value === undefined) delete options[fieldId];
    else options[fieldId] = value;
  }

  // switchKind resets the settings: nothing from one kind's schema means
  // anything to another's.
  function switchKind(kind: string) {
    form.kind = kind;
    options = defaultOptions(kind);
  }

  function validateForm(): string {
    if (!form.name.trim()) return 'Name is required';
    for (const field of kindDef?.options ?? []) {
      if (!field.required) continue;
      const v = options[field.id];
      const empty =
        v === undefined || v === null ||
        (typeof v === 'string' && v.trim() === '') ||
        (Array.isArray(v) && v.length === 0);
      if (empty) return `${field.label} is required`;
    }
    return '';
  }

  async function save() {
    formError = validateForm() || historyField?.validate() || '';
    if (formError) return;
    saving = true;
    let saved: Control;
    try {
      const body = {
        name: form.name.trim(),
        kind: form.kind,
        icon: form.icon.trim(),
        options: { ...options },
        allow_public: form.allow_public,
      };
      saved = editing
        ? await controlsStore.update(editing.id, body)
        : await controlsStore.create(body);
    } catch (e: any) {
      formError = e.message || 'Failed to save control';
      saving = false;
      return;
    }
    // The control is saved by now, so a history failure keeps the dialog open
    // on the saved control and a retry does not create it twice.
    try {
      await historyField?.commit(saved.id, saved.name);
      showModal = false;
    } catch (e: any) {
      editing = saved;
      formError = `The control was saved, but its history was not: ${e.message || 'unknown error'}`;
    } finally {
      saving = false;
    }
  }

  async function remove(control: Control) {
    const ok = await confirm(
      `Delete control "${control.name}"? Flow nodes and dashboard rows pointing at it will stop resolving.`,
    );
    if (!ok) return;
    error = '';
    try {
      await controlsStore.remove(control.id);
    } catch (e: any) {
      error = e.message || 'Failed to delete control';
    }
  }

  // set writes a value from the inline face in the table, which doubles as a
  // preview of what a dashboard row does with the same control. An attribute
  // names one part of a composite control, so a light's brightness moves
  // without disturbing its colour.
  let busyId = $state('');
  async function set(control: Control, value: unknown, attribute?: string) {
    error = '';
    busyId = control.id;
    try {
      await controlsStore.set(control.id, value, attribute);
    } catch (e: any) {
      error = e.message || 'Failed to set the control';
    } finally {
      busyId = '';
    }
  }

  // resolvedOf shapes a control the way the face expects a source: the same
  // {kind, options, value} a dashboard row hands it.
  function resolvedOf(control: Control): Resolved {
    return {
      kind: control.kind,
      options: control.options || {},
      readOnly: false,
      name: control.name,
      value: control.value,
    };
  }

  function kindLabel(name: string): string {
    return kinds.find((k) => k.name === name)?.label || name;
  }

  function describeKind(kind: ControlKind | undefined): string {
    return kind?.description || '';
  }
</script>

<h2 class="text-xl font-bold mb-2">Controls</h2>
<p class="text-sm mb-6" style="color: var(--theme-text-muted, #a6adc8)">
  Values karakuri owns rather than reads from a device: a toggle, a number, a
  dropdown, a row of buttons, a light. A dashboard row sets one and any number
  of flows read it with a Control Value node or trigger on it with Control
  Changed, so the dashboard and the automation never have to know about each
  other. The kinds here are the same ones an MQTT device picks from, so a light
  is drawn the same way whichever side of the house it comes from.
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
      <i class="las la-plus mr-1"></i>Add Control
    </button>
  </div>
{/if}

{#if loading}
  <p class="opacity-60">Loading controls...</p>
{:else if controls.length === 0}
  <p class="opacity-60 text-center py-8">
    No controls yet. Add one, then place it on a dashboard or read it from a flow.
  </p>
{:else}
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
          <th class="py-2 pr-4 text-left">Name</th>
          <th class="py-2 pr-4 text-left">Kind</th>
          <th class="py-2 pr-4 text-left">Value</th>
          <th class="py-2 pr-4 text-left">Public</th>
          <th class="py-2 text-right">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each controls as control (control.id)}
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <td class="py-2 pr-4 font-medium">
              {#if control.icon}<i class="las {control.icon} mr-1"></i>{/if}{control.name}
            </td>
            <td class="py-2 pr-4 text-xs opacity-70">{kindLabel(control.kind)}</td>
            <td class="py-2 pr-4">
              <div class="value-cell">
                <ControlFace
                  resolved={resolvedOf(control)}
                  busy={busyId === control.id}
                  command={(attribute, value) => set(control, value, attribute)}
                />
              </div>
            </td>
            <td class="py-2 pr-4 text-xs opacity-70">
              {control.allow_public ? 'yes' : 'no'}
            </td>
            <td class="py-2 text-right whitespace-nowrap">
              {#if canWrite}
                <button class="icon-btn" title="Edit" onclick={() => openEdit(control)}>
                  <i class="la la-pen"></i>
                </button>
                <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => remove(control)}>
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
  <Modal title={editing ? 'Edit Control' : 'New Control'} onclose={() => (showModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); save(); }} class="flex flex-col gap-3">
      {#if formError}
        <div class="p-2 rounded text-sm"
             style="background-color: var(--theme-error); color: var(--theme-base);">
          {formError}
        </div>
      {/if}
      <FormField label="Name" id="control-name">
        <input id="control-name" class="input w-full" bind:value={form.name}
               placeholder="e.g. House Mode" required />
      </FormField>
      <FormField label="Kind" id="control-kind">
        <select
          id="control-kind"
          class="input w-full"
          value={form.kind}
          onchange={(e) => switchKind(e.currentTarget.value)}
        >
          {#each kinds as k (k.name)}
            <option value={k.name}>{k.label}</option>
          {/each}
        </select>
        {#if kindDef}
          <p class="text-xs mt-1" style="color: var(--theme-text-muted, #a6adc8)">
            {describeKind(kindDef)}
            {#if editing && editing.kind !== form.kind}
              <span style="color: var(--theme-warning, #f9e2af)">
                Changing the kind resets the current value.
              </span>
            {/if}
          </p>
        {/if}
      </FormField>
      <FormField label="Icon" id="control-icon">
        <input id="control-icon" class="input w-full" bind:value={form.icon}
               placeholder="e.g. la-lightbulb" />
      </FormField>
      <!-- The kind's own settings come from the backend as a field schema, so a
           new kind renders here with no change. -->
      {#if kindDef && (kindDef.options ?? []).length > 0}
        <ConfigForm
          spec={kindDef.options}
          values={options}
          idPrefix="control"
          onChange={setOption}
        />
      {/if}
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" bind:checked={form.allow_public} />
        <span>
          Let anonymous viewers of a public dashboard read and set it
        </span>
      </label>
      {#if canRecordHistory}
        <HistoryRecordField
          bind:this={historyField}
          sourceType="control"
          sourceId={editing?.id ?? null}
        />
      {/if}
      <div class="flex gap-2 justify-end">
        <button type="button" class="btn btn-secondary" onclick={() => (showModal = false)}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={saving}>
          {saving ? 'Saving...' : editing ? 'Save' : 'Create'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  /* The face stacks a composite kind's attributes; the cap keeps a light's
     sliders from stretching across the whole table. */
  .value-cell {
    max-width: 22rem;
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
