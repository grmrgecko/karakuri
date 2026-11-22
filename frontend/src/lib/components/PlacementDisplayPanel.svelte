<script lang="ts">
  import { REGIONS, regionOf } from '../regionLayout';
  import type {
    Condition,
    CueAnimation,
    DashboardVariable,
    DashboardPage,
    DashboardWidget,
    StatusCue,
    TileStyle,
    WidgetDisplay,
  } from '../stores/dashboards';
  import ConditionListEditor from './ConditionListEditor.svelte';

  // PlacementDisplayPanel edits the host-owned half of a placement: the page
  // and region it sits in and its display settings. The widget's own config
  // panel sits below it; neither writes the other's fields.
  let {
    widget,
    pages,
    layoutMode,
    variables = [],
    onchange,
    onmove,
  }: {
    widget: DashboardWidget;
    pages: DashboardPage[];
    layoutMode: string;
    variables?: DashboardVariable[];
    onchange: (patch: { page_id?: string; region?: string; display?: WidgetDisplay }) => void;
    onmove?: (delta: number) => void;
  } = $props();

  const animations = [
    { value: '', label: 'Default (fade)' },
    { value: 'none', label: 'None' },
    { value: 'fade', label: 'Fade' },
    { value: 'slide-up', label: 'Slide up' },
    { value: 'slide-down', label: 'Slide down' },
    { value: 'slide-left', label: 'Slide left' },
    { value: 'slide-right', label: 'Slide right' },
    { value: 'zoom', label: 'Zoom' },
  ];
  const cueAnimations: { value: CueAnimation; label: string }[] = [
    { value: 'static', label: 'Steady' },
    { value: 'pulse', label: 'Pulse' },
    { value: 'blink', label: 'Blink' },
    { value: 'breathe', label: 'Breathe' },
    { value: 'comet', label: 'Comet' },
    { value: 'ants', label: 'Marching ants' },
  ];
  const MAX_CUES = 8;

  let display = $derived<WidgetDisplay>(widget.display ?? {});
  let conditions = $derived<Condition[]>(display.conditions ?? []);
  let style = $derived<TileStyle>(display.style ?? {});
  let cues = $derived<StatusCue[]>(display.cues ?? []);

  function patchDisplay(next: Partial<WidgetDisplay>) {
    onchange({ display: { ...display, ...next } });
  }

  // patchStyle drops cleared fields, and the style itself once it is empty,
  // so a placement whose style was undone stores as one that never had it.
  function patchStyle(next: Partial<TileStyle>) {
    const merged: Record<string, unknown> = { ...style, ...next };
    for (const key of Object.keys(merged)) {
      if (merged[key] === undefined || merged[key] === '') delete merged[key];
    }
    patchDisplay({ style: Object.keys(merged).length ? (merged as TileStyle) : undefined });
  }

  function patchCue(index: number, next: Partial<StatusCue>) {
    patchDisplay({ cues: cues.map((c, i) => (i === index ? { ...c, ...next } : c)) });
  }

  function moveCue(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= cues.length) return;
    const list = [...cues];
    [list[index], list[target]] = [list[target], list[index]];
    patchDisplay({ cues: list });
  }

  function removeCue(index: number) {
    const list = cues.filter((_, i) => i !== index);
    patchDisplay({ cues: list.length ? list : undefined });
  }

  const value = (e: Event) => (e.currentTarget as HTMLInputElement | HTMLSelectElement).value;

  // numberOrUnset reads a number field, leaving it unset when blank.
  function numberOrUnset(e: Event, max: number): number | undefined {
    const raw = value(e);
    const n = Number(raw);
    if (raw === '' || !Number.isFinite(n)) return undefined;
    return Math.max(0, Math.min(max, Math.round(n)));
  }
</script>

{#snippet colorField(label: string, current: string | undefined, set: (v: string | undefined) => void)}
  <label class="mini">
    <span>{label}</span>
    <span class="flex items-center gap-1">
      <input type="color" class="swatch" value={/^#[0-9a-f]{6}$/i.test(current ?? '') ? current : '#888888'}
        aria-label="{label} color" onchange={(e) => set(value(e))} />
      <input class="input text-xs flex-1 min-w-0" placeholder="Theme" value={current ?? ''}
        aria-label="{label} CSS color" onchange={(e) => set(value(e).trim() || undefined)} />
    </span>
  </label>
{/snippet}

<section class="display-panel">
  <h4 class="panel-heading">Display</h4>

  <label class="field">
    <span>Name</span>
    <input class="input w-full text-sm" value={display.name ?? ''} placeholder={widget.widget?.name ?? ''}
      onchange={(e) => patchDisplay({ name: value(e).trim() || undefined })} />
    <small>Shown in the widget pickers of the Dashboard Command node.</small>
  </label>

  {#if pages.length > 0}
    <label class="field">
      <span>Page</span>
      <select class="input w-full text-sm" value={widget.page_id ?? ''} onchange={(e) => onchange({ page_id: value(e) })}>
        <option value="">Every page</option>
        {#each pages as page (page.id)}
          <option value={page.id}>{page.title}</option>
        {/each}
      </select>
    </label>
  {/if}

  {#if layoutMode === 'regions'}
    <div class="field">
      <span>Region</span>
      <div class="flex gap-1">
        <select class="input flex-1 text-sm" value={regionOf(widget)} onchange={(e) => onchange({ region: value(e) })}>
          {#each REGIONS as region (region.id)}
            <option value={region.id}>{region.label}</option>
          {/each}
        </select>
        <button class="btn btn-secondary btn-sm" title="Move up in the region" aria-label="Move up in the region" onclick={() => onmove?.(-1)}>
          <i class="las la-arrow-up"></i>
        </button>
        <button class="btn btn-secondary btn-sm" title="Move down in the region" aria-label="Move down in the region" onclick={() => onmove?.(1)}>
          <i class="las la-arrow-down"></i>
        </button>
      </div>
      <small>Width is in grid columns of the region; height is in rows of the row height.</small>
    </div>
  {/if}

  <label class="check">
    <input type="checkbox" checked={!!display.hidden} onchange={(e) => patchDisplay({ hidden: (e.currentTarget as HTMLInputElement).checked || undefined })} />
    <span>Hidden on start</span>
  </label>
  <small class="hint">Starts under the lock <code>start</code>. A Dashboard Command showing the lock <code>start</code>, or forcing, reveals it.</small>

  <div class="field">
    <div class="flex items-center justify-between">
      <span>Show only when</span>
      {#if conditions.length > 1}
        <select class="input text-xs" value={display.match ?? 'all'} onchange={(e) => patchDisplay({ match: value(e) === 'any' ? 'any' : undefined })}>
          <option value="all">all conditions pass</option>
          <option value="any">any condition passes</option>
        </select>
      {/if}
    </div>
    <ConditionListEditor {conditions} {variables} idPrefix="{widget.id}-show"
      onchange={(next) => patchDisplay({ conditions: next.length ? next : undefined })} />
    {#if conditions.some((c) => c.kind === 'state')}
      <small>Viewers of this dashboard can read the sources named here, as they can a display-only row. They cannot operate them.</small>
    {/if}
  </div>

  <div class="field">
    <span>Tile style</span>
    <label class="check">
      <input type="checkbox" checked={style.background === 'transparent'}
        onchange={(e) => patchStyle({ background: (e.currentTarget as HTMLInputElement).checked ? 'transparent' : undefined })} />
      <span>Transparent (no face, border, or shadow)</span>
    </label>
    <div class="style-grid">
      {#if style.background !== 'transparent'}
        {@render colorField('Background', style.background, (v) => patchStyle({ background: v }))}
      {/if}
      {@render colorField('Border', style.border_color, (v) => patchStyle({ border_color: v }))}
      {@render colorField('Text', style.text_color, (v) => patchStyle({ text_color: v }))}
      {@render colorField('Accent', style.accent_color, (v) => patchStyle({ accent_color: v }))}
      <label class="mini">
        <span>Border width</span>
        <input type="number" class="input text-xs" min="0" max="16" placeholder="Theme"
          value={style.border_width ?? ''} onchange={(e) => patchStyle({ border_width: numberOrUnset(e, 16) || undefined })} />
      </label>
      <label class="mini">
        <span>Corner radius</span>
        <input type="number" class="input text-xs" min="0" max="64" placeholder="Theme"
          value={style.radius ?? ''} onchange={(e) => patchStyle({ radius: numberOrUnset(e, 64) })} />
      </label>
    </div>
    <small>Empty fields follow the theme. Widgets that use the theme's text and accent colors take these.</small>
  </div>

  <div class="field">
    <span>Status cues</span>
    {#each cues as cue, i (i)}
      <div class="cue">
        <div class="flex items-center gap-1">
          <select class="input flex-1 text-xs" value={cue.animation ?? 'static'} aria-label="Cue animation"
            onchange={(e) => patchCue(i, { animation: value(e) === 'static' ? undefined : (value(e) as CueAnimation) })}>
            {#each cueAnimations as a (a.value)}
              <option value={a.value}>{a.label}</option>
            {/each}
          </select>
          <input type="color" class="swatch" value={/^#[0-9a-f]{6}$/i.test(cue.color ?? '') ? cue.color : '#f9e2af'}
            aria-label="Cue color" onchange={(e) => patchCue(i, { color: value(e) })} />
          <button class="icon-btn" title="Move up" aria-label="Move cue up" disabled={i === 0} onclick={() => moveCue(i, -1)}>
            <i class="las la-arrow-up"></i>
          </button>
          <button class="icon-btn" title="Move down" aria-label="Move cue down" disabled={i === cues.length - 1} onclick={() => moveCue(i, 1)}>
            <i class="las la-arrow-down"></i>
          </button>
          <button class="icon-btn danger" title="Remove cue" aria-label="Remove cue" onclick={() => removeCue(i)}>
            <i class="las la-trash"></i>
          </button>
        </div>
        <div class="flex items-center justify-between mt-1">
          <small>{(cue.conditions ?? []).length === 0 ? 'Always draws.' : 'Draws when'}</small>
          {#if (cue.conditions ?? []).length > 1}
            <select class="input text-xs" value={cue.match ?? 'all'} onchange={(e) => patchCue(i, { match: value(e) === 'any' ? 'any' : undefined })}>
              <option value="all">all pass</option>
              <option value="any">any passes</option>
            </select>
          {/if}
        </div>
        <ConditionListEditor conditions={cue.conditions ?? []} {variables} idPrefix="{widget.id}-cue{i}"
          onchange={(next) => patchCue(i, { conditions: next.length ? next : undefined })} />
      </div>
    {/each}
    {#if cues.length < MAX_CUES}
      <button class="btn btn-secondary btn-sm self-start" onclick={() => patchDisplay({ cues: [...cues, { color: '#f38ba8', animation: 'pulse' }] })}>
        <i class="las la-plus mr-1"></i>Cue
      </button>
    {/if}
    <small>The first cue whose conditions pass draws a border around the tile. Viewers can read the sources a cue names, as they can a display-only row.</small>
  </div>

  <div class="field">
    <span>Animation</span>
    <div class="grid grid-cols-2 gap-1">
      <select class="input text-xs" value={display.animate_in ?? ''} aria-label="Enter animation" onchange={(e) => patchDisplay({ animate_in: value(e) || undefined })}>
        {#each animations as a (a.value)}
          <option value={a.value}>In: {a.label}</option>
        {/each}
      </select>
      <select class="input text-xs" value={display.animate_out ?? ''} aria-label="Exit animation" onchange={(e) => patchDisplay({ animate_out: value(e) || undefined })}>
        {#each animations as a (a.value)}
          <option value={a.value}>Out: {a.label}</option>
        {/each}
      </select>
    </div>
    <input type="number" class="input w-full text-xs mt-1" min="0" max="5000" step="50" placeholder="Duration in ms, default 300"
      value={display.duration ?? ''} onchange={(e) => { const n = Number(value(e)); patchDisplay({ duration: value(e) === '' || !Number.isFinite(n) ? undefined : Math.max(0, Math.min(5000, Math.round(n))) }); }} />
  </div>
</section>

<style>
  .display-panel {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    padding-bottom: 0.9rem;
    margin-bottom: 0.9rem;
    border-bottom: 1px solid var(--theme-surface0, #313244);
  }

  .panel-heading {
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    opacity: 0.6;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.8rem;
  }

  .field small, .hint {
    font-size: 0.7rem;
    opacity: 0.6;
  }

  .hint {
    margin-top: -0.4rem;
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }

  .cue {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    padding: 0.45rem;
    border: 1px solid var(--theme-surface0, #313244);
    border-radius: var(--theme-radius-sm, 4px);
  }

  .style-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.35rem;
  }

  .mini {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    font-size: 0.7rem;
    min-width: 0;
  }

  .swatch {
    width: 1.6rem;
    height: 1.6rem;
    padding: 0;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: transparent;
    flex-shrink: 0;
    cursor: pointer;
  }

  .icon-btn {
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0 0.3rem;
  }

  .icon-btn:hover {
    color: var(--theme-accent, #89b4fa);
  }

  .icon-btn.danger:hover {
    color: var(--theme-error, #f38ba8);
  }

  .icon-btn:disabled {
    opacity: 0.3;
    cursor: default;
  }

</style>
