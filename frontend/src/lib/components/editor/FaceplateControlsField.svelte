<script module lang="ts">
  // openControl remembers which control is expanded by ID, outside the
  // component, so the panel redrawing after a change keeps it open.
  let openControl = '';
</script>

<script lang="ts">
  import { bundledTemplates } from '../../faceplateTemplates';

  // FaceplateControlsField edits a faceplate's control list. Each control
  // becomes a value output named by its ID, so renaming an ID rewires nothing
  // and the editor keeps IDs stable once set.
  interface Option { value: unknown; label?: string }
  interface Control {
    id: string;
    label: string;
    type: string;
    description?: string;
    default?: unknown;
    min?: number;
    max?: number;
    step?: number;
    options?: Option[];
    template?: string;
  }

  let { value, onChange }: { value: unknown; onChange: (controls: Control[]) => void } = $props();
  let controls = $derived((Array.isArray(value) ? value : []) as Control[]);
  let openId = $state(openControl);
  function setOpen(id: string) {
    openId = id;
    openControl = id;
  }

  const types = [
    { value: 'number', label: 'Number' },
    { value: 'boolean', label: 'On / off' },
    { value: 'string', label: 'Text or choice' },
    { value: 'entity', label: 'Home Assistant entity' },
    { value: 'array', label: 'List (template)' },
    { value: 'object', label: 'Object (template)' },
  ];

  function update(index: number, patch: Partial<Control>) {
    onChange(controls.map((c, i) => {
      if (i !== index) return c;
      const next: Control = { ...c, ...patch };
      for (const k of Object.keys(next) as (keyof Control)[]) {
        if (next[k] === undefined || next[k] === '') delete next[k];
      }
      return next;
    }));
  }

  function slug(label: string): string {
    const base = label.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'setting';
    const start = /^[a-z]/.test(base) ? base : `c_${base}`;
    let id = start.slice(0, 60);
    let n = 2;
    while (controls.some((c) => c.id === id)) id = `${start.slice(0, 56)}_${n++}`;
    return id;
  }

  function add() {
    // A new control opens and every other one closes, so the fields shown are
    // the new control's.
    const id = slug('setting');
    onChange([...controls, { id, label: 'Setting', type: 'number', default: 0 }]);
    setOpen(id);
  }

  function move(index: number, direction: number) {
    const next = [...controls];
    [next[index], next[index + direction]] = [next[index + direction], next[index]];
    onChange(next);
  }

  function num(raw: string): number | undefined {
    if (raw.trim() === '') return undefined;
    const n = Number(raw);
    return Number.isFinite(n) ? n : undefined;
  }

  // Options are written one per line as "value" or "value | label".
  function optionsText(c: Control): string {
    return (c.options ?? []).map((o) => (o.label ? `${o.value} | ${o.label}` : String(o.value))).join('\n');
  }
  function parseOptions(text: string, type: string): Option[] | undefined {
    const out: Option[] = [];
    for (const line of text.split('\n')) {
      const [raw, label] = line.split('|').map((s) => s.trim());
      if (!raw) continue;
      const v = type === 'number' ? Number(raw) : raw;
      out.push(label ? { value: v, label } : { value: v });
    }
    return out.length ? out : undefined;
  }

  function defaultText(c: Control): string {
    if (c.default === undefined || c.default === null) return '';
    return typeof c.default === 'string' ? c.default : JSON.stringify(c.default);
  }
  function setDefault(index: number, c: Control, raw: string) {
    if (raw.trim() === '') return update(index, { default: undefined });
    if (c.type === 'number') return update(index, { default: num(raw) });
    if (c.type === 'string') return update(index, { default: raw });
    try {
      update(index, { default: JSON.parse(raw) });
    } catch {
      // Leave the stored default until the text parses.
    }
  }

  // starterTemplate is the editor's first draft of a custom template. The
  // closing tag is split so it does not end this component's own script.
  const starterTemplate = [
    '<div id="app"></div>',
    '<' + 'script>',
    '  karakuri.onUpdate((k) => {',
    '    document.getElementById("app").textContent = JSON.stringify(k.value);',
    '  });',
    '</' + 'script>',
  ].join('\n');

  async function editTemplate(index: number, c: Control) {
    const edited = await (window as any).__karakuri?.editDocument({
      title: `Template for ${c.label || c.id}`,
      value: c.template ?? starterTemplate,
      format: 'plain',
      saveLabel: 'Save template',
      hint: 'Use window.karakuri: value, onUpdate(fn), setValue(v). Runs isolated, with no network.',
    });
    if (edited !== null && edited !== undefined) update(index, { template: edited || undefined });
  }

  function pickTemplate(index: number, id: string) {
    if (id === '') return update(index, { template: undefined });
    const t = bundledTemplates.find((b) => b.id === id);
    if (t) update(index, { template: t.html, type: t.type });
  }
</script>

<div class="flex flex-col gap-2">
  {#each controls as c, i (i)}
    <div class="border border-surface1 rounded p-2">
      <div class="flex items-center gap-1">
        <button type="button" class="summary" title={openId === c.id ? 'Collapse' : 'Edit this control'}
          aria-expanded={openId === c.id} onclick={() => setOpen(openId === c.id ? '' : c.id)}>
          <i class="las {openId === c.id ? 'la-angle-down' : 'la-angle-right'}"></i>
          <span class="truncate text-sm">{c.label || 'Untitled control'}</span>
          <span class="text-xs text-text-muted truncate">{c.id} · {types.find((t) => t.value === c.type)?.label ?? c.type}</span>
        </button>
        <button type="button" class="mini" title="Move up" disabled={i === 0} onclick={() => move(i, -1)}><i class="las la-arrow-up"></i></button>
        <button type="button" class="mini" title="Move down" disabled={i === controls.length - 1} onclick={() => move(i, 1)}><i class="las la-arrow-down"></i></button>
        <button type="button" class="mini" aria-label={`Remove control ${i + 1}`} title="Remove" onclick={() => onChange(controls.filter((_, index) => index !== i))}><i class="las la-times"></i></button>
      </div>
      {#if openId === c.id}
        <div class="flex flex-col gap-2 mt-2 text-xs">
          <label class="block text-text-muted">Label
            <input class="input w-full text-sm mt-1" value={c.label} placeholder="Max brightness"
              oninput={(e) => update(i, { label: e.currentTarget.value })} />
          </label>
          <label class="block text-text-muted">Output ID
            <input class="input w-full text-xs mt-1" value={c.id} placeholder="max_brightness" onchange={(e) => { const id = e.currentTarget.value.trim(); update(i, { id }); setOpen(id); }} />
          </label>
          <label class="block text-text-muted">Type
            <select class="input w-full text-xs mt-1" value={c.type} onchange={(e) => update(i, { type: e.currentTarget.value, default: undefined, options: undefined })}>
              {#each types as t (t.value)}<option value={t.value}>{t.label}</option>{/each}
            </select>
          </label>
          <label class="block text-text-muted">Description
            <input class="input w-full text-xs mt-1" value={c.description ?? ''} placeholder="Shown under the control" oninput={(e) => update(i, { description: e.currentTarget.value })} />
          </label>
          {#if c.type === 'boolean'}
            <label class="flex items-center gap-2 text-text-muted">
              <input type="checkbox" checked={c.default === true} onchange={(e) => update(i, { default: e.currentTarget.checked })} /> On by default
            </label>
          {:else}
            <label class="block text-text-muted">Default{c.type === 'entity' ? ' (JSON: {"connection_id": "...", "entity_id": "sensor.lux"})' : c.type === 'array' || c.type === 'object' ? ' (JSON)' : ''}
              <input class="input w-full text-xs mt-1 font-mono" value={defaultText(c)} onchange={(e) => setDefault(i, c, e.currentTarget.value)} />
            </label>
          {/if}
          {#if c.type === 'number'}
            <div class="grid grid-cols-3 gap-2">
              <label class="block text-text-muted">Min<input class="input w-full text-xs mt-1" type="number" value={c.min ?? ''} onchange={(e) => update(i, { min: num(e.currentTarget.value) })} /></label>
              <label class="block text-text-muted">Max<input class="input w-full text-xs mt-1" type="number" value={c.max ?? ''} onchange={(e) => update(i, { max: num(e.currentTarget.value) })} /></label>
              <label class="block text-text-muted">Step<input class="input w-full text-xs mt-1" type="number" value={c.step ?? ''} onchange={(e) => update(i, { step: num(e.currentTarget.value) })} /></label>
            </div>
          {/if}
          {#if c.type === 'string' || c.type === 'number'}
            <label class="block text-text-muted">Choices (one per line, "value | label"; leave empty for free entry)
              <textarea class="input w-full text-xs mt-1 font-mono" rows="3" value={optionsText(c)} onchange={(e) => update(i, { options: parseOptions(e.currentTarget.value, c.type) })}></textarea>
            </label>
          {/if}
          <div class="flex items-center gap-2 text-text-muted">
            <span>Custom editor</span>
            <select class="input text-xs flex-1" value={bundledTemplates.find((b) => b.html === c.template)?.id ?? (c.template ? 'custom' : '')}
              onchange={(e) => e.currentTarget.value !== 'custom' && pickTemplate(i, e.currentTarget.value)}>
              <option value="">None</option>
              {#each bundledTemplates as t (t.id)}<option value={t.id}>{t.label}</option>{/each}
              {#if c.template && !bundledTemplates.some((b) => b.html === c.template)}<option value="custom">Custom</option>{/if}
            </select>
            <button type="button" class="btn p-1 text-xs" onclick={() => editTemplate(i, c)}>Edit HTML</button>
          </div>
        </div>
      {/if}
    </div>
  {/each}
  <button type="button" class="btn text-sm" disabled={controls.length >= 64} onclick={add}>Add control</button>
</div>

<style>
  .summary {
    display: flex;
    align-items: baseline;
    gap: 0.4rem;
    flex: 1;
    min-width: 0;
    text-align: left;
    padding: 0.15rem 0.2rem;
    border-radius: 0.25rem;
  }
  .summary:hover {
    background-color: var(--theme-surface1);
  }
  .mini {
    padding: 0.15rem 0.3rem;
    border-radius: 0.25rem;
    opacity: 0.7;
  }
  .mini:hover:not(:disabled) {
    opacity: 1;
    background-color: var(--theme-surface1);
  }
  .mini:disabled {
    opacity: 0.25;
  }
</style>
