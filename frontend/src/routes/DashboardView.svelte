<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { dashboards } from '../lib/stores/dashboards';
  import { themes } from '../lib/stores/themes';
  import { api } from '../lib/api/client';
  import { setDashboardContext } from '../lib/dashboardContext';
  import { widgetRect, DEFAULT_ROW_HEIGHT } from '../lib/grid';
  import { computeRegions } from '../lib/dashboardLayout';
  import type { LayoutRegion } from '../lib/dashboardLayout';
  import { watchLive, watchConditionSources } from '../lib/dashboardLive';
  import {
    dimLevel,
    emptyLiveState,
    isVisible,
    orderedPages,
    stepPage,
    wakeSeconds,
    activeCue,
    displayConditions,
  } from '../lib/displayState';
  import type { PrimaryAttributes } from '../lib/displayState';
  import { presence } from '../lib/presence';
  import { isTransparent, tileStyleCSS } from '../lib/tileStyle';
  import TileCue from '../lib/components/TileCue.svelte';
  import {
    chainedDependencies,
    defaultValue,
    displayOf,
    follows,
    initialValues,
    isPinned,
    loadVariables,
    optionOf,
    optionsOf,
    optionText,
    pinnedNames,
    referencesOf,
    sharedValues,
    substituteCondition,
    substituteConfig,
    writeAddress,
  } from '../lib/variables';
  import type { VariableValues } from '../lib/variables';
  import { websocket } from '../lib/stores/websocket';
  import { publishOperate, publishVariables } from '../lib/widgetBridge';
  import type { RegionId } from '../lib/regionLayout';
  import WidgetRenderer from '../lib/components/WidgetRenderer.svelte';
  import TimeRangePicker from '../lib/components/TimeRangePicker.svelte';
  import RegionLayout from '../lib/components/RegionLayout.svelte';
  import DashboardAlerts from '../lib/components/DashboardAlerts.svelte';
  import type { Condition, Dashboard, DashboardWidget, LiveState, ResolvedVariables } from '../lib/stores/dashboards';

  let { id }: { id: string } = $props();

  let dashboard = $state<Dashboard | null>(null);
  let containerEl = $state<HTMLDivElement | undefined>(undefined);
  let containerWidth = $state(0);
  let containerHeight = $state(0);
  let loading = $state(true);

  // A headless render photographs the dashboard as it stands: nothing that
  // only makes sense on a screen someone looks at over time applies to it.
  const rendering = api.isRenderMode();

  // A viewer may fold a section open or shut for themselves. The dashboard's
  // own collapsed flag is the author's default, and this never persists.
  let collapsedOverride = $state<Record<string, boolean>>({});

  let scrolling = $derived(dashboard?.layout_mode === 'scroll');
  let regionMode = $derived(dashboard?.layout_mode === 'regions');

  let gridConfig = $derived(dashboard
    ? {
        rows: dashboard.grid_rows,
        cols: dashboard.grid_cols,
        containerWidth,
        containerHeight,
        padding: dashboard.padding ?? 8,
        gap: dashboard.gap ?? 8,
        mode: dashboard.layout_mode === 'scroll' ? ('scroll' as const) : ('fit' as const),
        rowHeight: dashboard.row_height || DEFAULT_ROW_HEIGHT,
      }
    : null);

  let layout = $derived(gridConfig && dashboard && !regionMode
    ? computeRegions(dashboard.widgets ?? [], dashboard.sections ?? [], gridConfig, { collapsedOverride })
    : null);

  // ---- Runtime display state ----

  let live = $state<LiveState>(emptyLiveState());
  let records = $state<Record<string, unknown>>({});
  // primaryAttributes lets a state condition on a composite control compare
  // its primary attribute when the condition names none.
  let primaryAttributes = $state<PrimaryAttributes>({});
  api.controls
    .kinds<any>()
    .then((res) => {
      const list = Array.isArray(res) ? res : (res?.data ?? []);
      const next: PrimaryAttributes = {};
      for (const k of list) {
        if (k?.value_type !== 'object') continue;
        const primary = (k.attributes ?? []).find((a: any) => a.primary)?.id;
        if (primary) next[k.name] = primary;
      }
      primaryAttributes = next;
    })
    .catch(() => {});
  let now = $state(new Date());
  let pages = $derived(orderedPages(dashboard?.pages));
  let currentPage = $state('');
  let liveLoaded = $state(false);

  // ---- Variables ----

  // Each viewer holds its own value per variable. It starts from the address,
  // and after that the latest of the viewer's pick and a dashboard-wide change
  // wins, unless the address pins the variable.
  const search = new URLSearchParams(window.location.search);
  const pins = pinnedNames(search);
  // resolved holds the server's resolution, which is what query options and
  // followed entities come from; until it arrives the stored list stands in.
  let resolved = $state<ResolvedVariables | null>(null);
  let variableDefs = $derived(resolved?.variables ?? dashboard?.display?.variables ?? []);
  let followed = $state<Record<string, string>>({});
  let barPickers = $derived(rendering ? [] : variableDefs.filter((v) => ['above', 'label_hidden'].includes(displayOf(v))));
  let menuPickers = $derived(rendering ? [] : variableDefs.filter((v) => displayOf(v) === 'menu'));
  let menuOpen = $state(false);
  let variableValues = $state<VariableValues>({});

  function pickVariable(name: string, value: string) {
    if (!variableDefs.some((v) => v.name === name && optionOf(v, value))) return;
    variableValues = { ...variableValues, [name]: value };
    writeAddress(name, value);
    // A query that refers to this variable re-resolves for the new value;
    // a dependent value that is no longer an option falls back to the
    // default.
    const dependents = chainedDependencies(variableDefs).get(name);
    if (dependents?.length) void refreshVariables(false, dependents);
  }

  // Widgets such as Variable buttons and the news feed read and set this
  // screen's values through the bridge.
  $effect(() => {
    publishVariables({ dashboardId: dashboard?.id ?? '', variables: variableDefs, values: variableValues, setValue: pickVariable });
  });

  // adoptShared takes the dashboard-wide value of each variable that changed
  // between two states. Variables left alone keep this viewer's own pick.
  function adoptShared(before: VariableValues, after: VariableValues) {
    const changed = Object.keys(after).filter((name) => after[name] !== before[name] && !isPinned(pins, name));
    if (changed.length === 0) return;
    const merged = { ...variableValues };
    for (const name of changed) merged[name] = after[name];
    variableValues = merged;
  }

  function adoptVariableCommand(previous: LiveState, next: LiveState) {
    adoptShared(sharedValues(variableDefs, previous, followed), sharedValues(variableDefs, next, followed));
  }

  // A variable that follows a select entity takes its state as the
  // dashboard-wide value. The layout grants that entity's state topic.
  $effect(() => {
    const mappings = resolved?.follow_map ?? {};
    const followers = variableDefs.filter(follows);
    if (followers.length === 0) return;
    const offs = followers.map((v) =>
      websocket.subscribeTopic(`ha:${v.query!.connection_id}:state:${v.query!.entity_id}`, (data: any) => {
        const raw = data?.new_state?.state;
        const value = typeof raw === 'string' ? mappings[v.name]?.[raw] : undefined;
        if (!value || !optionOf(v, value) || followed[v.name] === value) return;
        const before = sharedValues(variableDefs, live, followed);
        followed = { ...followed, [v.name]: value };
        adoptShared(before, sharedValues(variableDefs, live, followed));
      })
    );
    return () => offs.forEach((off) => off());
  });

  // A reconnect refetches the resolution: a query's options may have changed
  // and a followed entity may have moved while the socket was down.
  let wasConnected = true;
  const offReconnect = websocket.subscribe((ws) => {
    if (ws.connected && !wasConnected && dashboard) void refreshVariables(true);
    wasConnected = ws.connected;
  });

  async function refreshVariables(adopt: boolean, dependents: string[] = []) {
    if (!dashboard) return;
    const before = sharedValues(variableDefs, live, followed);
    const next = await loadVariables(dashboard.id, search.get('view') === 'draft', dashboard.display?.variables ?? [], variableValues);
    resolved = next;
    followed = next.followed;
    if (adopt) adoptShared(before, sharedValues(next.variables, live, followed));
    if (dependents.length) {
      const fixed = { ...variableValues };
      for (const name of dependents) {
        const v = next.variables.find((x) => x.name === name);
        if (v && !optionOf(v, fixed[name])) fixed[name] = defaultValue(v);
      }
      variableValues = fixed;
    }
  }

  function cueOf(widget: DashboardWidget) {
    return activeCue(widget.display, records, now, { variables: variableDefs, values: variableValues }, primaryAttributes);
  }

  function visible(widget: DashboardWidget): boolean {
    return isVisible(widget, {
      state: live,
      currentPage,
      pages,
      records,
      now,
      variables: { variables: variableDefs, values: variableValues },
      primaryAttributes,
    });
  }

  // Alerts already on the dashboard when the viewer loads are not read aloud:
  // they were raised before anyone was listening here, and a reload must not
  // repeat them.
  const heardAlerts = new Set<string>();

  function speakNew(state: LiveState, initial: boolean) {
    for (const alert of state.alerts) {
      if (heardAlerts.has(alert.id)) continue;
      heardAlerts.add(alert.id);
      if (initial || !alert.speak || rendering || typeof speechSynthesis === 'undefined') continue;
      const text = [alert.title, alert.message].filter(Boolean).join('. ');
      if (text) speechSynthesis.speak(new SpeechSynthesisUtterance(text));
    }
  }

  function onLive(state: LiveState, event: string | undefined) {
    speakNew(state, !liveLoaded);
    const previous = live;
    live = state;
    const known = (page: string | undefined) => !!page && pages.some((p) => p.id === page);
    if (!liveLoaded) {
      liveLoaded = true;
      currentPage = known(state.page) ? state.page! : (pages[0]?.id ?? '');
      variableValues = initialValues(variableDefs, state, search, followed);
      return;
    }
    switch (event) {
      case undefined:
        // A refetch after a reconnect names no event. A page or variable
        // command sent while the socket was down shows only as a changed
        // state, so take each change as that command would have.
        if (state.page !== previous.page) currentPage = known(state.page) ? state.page! : (pages[0]?.id ?? '');
        adoptVariableCommand(previous, state);
        break;
      case 'variable':
        adoptVariableCommand(previous, state);
        break;
      case 'page':
        if (known(state.page)) currentPage = state.page!;
        else currentPage = pages[0]?.id ?? '';
        break;
      case 'next_page':
        currentPage = stepPage(pages, currentPage, 1);
        break;
      case 'previous_page':
        currentPage = stepPage(pages, currentPage, -1);
        break;
      case 'reload':
        if (!rendering) window.location.reload();
        break;
    }
  }

  // Conditions name their sources; following them is what lets a placement
  // appear the moment a door opens, and a cue's border start the moment it
  // does.
  let conditions = $derived<Condition[]>(
    (dashboard?.widgets ?? [])
      .flatMap((w) => displayConditions(w.display))
      .filter((c) => c.kind === 'state')
      .map((c) => substituteCondition(c, variableDefs, variableValues))
  );
  let conditionKey = $derived(JSON.stringify(conditions));

  let stopLive: (() => void) | null = null;
  $effect(() => {
    const key = conditionKey;
    if (!dashboard || !key || conditions.length === 0) return;
    return watchConditionSources(conditions, (next) => (records = next));
  });

  // The clock drives time conditions and the night window; a minute's
  // resolution is what both are written in.
  const clock = setInterval(() => (now = new Date()), 15000);

  // ---- Page rotation ----

  // Every page change restarts the timer, so a page a command or a viewer
  // jumped to gets the full interval.
  $effect(() => {
    const interval = dashboard?.display?.page_interval ?? 0;
    const page = currentPage;
    if (rendering || interval <= 0 || pages.length < 2 || !page) return;
    const timer = setTimeout(() => (currentPage = stepPage(pages, page, 1)), interval * 1000);
    return () => clearTimeout(timer);
  });

  // ---- Dimming and idle ----

  let lastInput = $state(Date.now());
  let awakeUntil = $state(0);
  let dim = $derived(rendering
    ? 0
    : dimLevel({
        display: dashboard?.display,
        override: live.dim,
        now,
        idleSeconds: (now.getTime() - lastInput) / 1000,
        awake: now.getTime() < awakeUntil,
      }));

  function onInput() {
    const t = Date.now();
    lastInput = t;
    awakeUntil = t + wakeSeconds(dashboard?.display) * 1000;
    now = new Date(t);
  }

  // A tap on a dark screen wakes it rather than pressing whatever is under
  // the finger.
  function wakeTap(e: Event) {
    e.preventDefault();
    e.stopPropagation();
    onInput();
  }

  // ---- Burn-in shift ----

  let shift = $state({ x: 0, y: 0 });
  $effect(() => {
    const max = dashboard?.display?.pixel_shift ?? 0;
    if (rendering || max <= 0) {
      shift = { x: 0, y: 0 };
      return;
    }
    const step = () => {
      const r = () => Math.round((Math.random() * 2 - 1) * max);
      shift = { x: r(), y: r() };
    };
    const timer = setInterval(step, 120000);
    return () => clearInterval(timer);
  });

  // markReady raises a flag on the document once every widget on the page has
  // stopped loading. A headless render waits for it before it photographs the
  // page, which is the difference between a screenshot of the dashboard and a
  // screenshot of its spinners. A widget still fetching its data marks its
  // element data-widget-busy. Custom elements mount a moment after they are
  // attached, so the page must be quiet on two checks in a row. A widget that
  // never settles stops holding the render up after the deadline.
  async function markReady() {
    const deadline = Date.now() + 15000;
    let quiet = 0;
    while (Date.now() < deadline) {
      await tick();
      if (!document.querySelector('[data-widget-state="loading"], [data-widget-busy]')) quiet++;
      else quiet = 0;
      if (quiet >= 2) break;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    document.documentElement.dataset.dashboardReady = 'true';
  }

  async function load() {
    await themes.list();
    dashboard = await dashboards.get(id, new URLSearchParams(window.location.search).get('view') === 'draft' ? 'draft' : 'deployed');
    publishOperate(dashboard?.can_operate !== false);
    // Widgets ask the host for the data their config names and know nothing
    // about permissions; naming the dashboard is what lets the server answer a
    // viewer who holds none. It is set before any widget mounts.
    setDashboardContext(dashboard?.id ?? null);
    if (dashboard?.theme_id) {
      themes.applyById(dashboard.theme_id);
    }
    if (dashboard) {
      currentPage = orderedPages(dashboard.pages)[0]?.id ?? '';
      // Resolve queries and seed from the address before the live state
      // arrives, so widgets mount with their values rather than rebinding a
      // moment later. Custom variables need no round trip.
      if ((dashboard.display?.variables ?? []).some((v) => v.type === 'query' || v.type === 'constant')) {
        await refreshVariables(false);
      }
      variableValues = initialValues(variableDefs, null, search, followed);
      // A query that refers to another variable was first resolved over every
      // value of it; resolve it again for the value this viewer starts on.
      const chained = [...new Set([...chainedDependencies(variableDefs).values()].flat())];
      if (chained.length) await refreshVariables(false, chained);
      stopLive = watchLive(dashboard.id, onLive);
    }
    loading = false;
    void markReady();
  }

  load();

  onDestroy(() => {
    stopLive?.();
    offReconnect();
    publishVariables({ dashboardId: '', variables: [], values: {} });
    publishOperate(true);
    clearInterval(clock);
    setDashboardContext(null);
    themes.applyById(null);
    delete document.documentElement.dataset.dashboardReady;
  });

  function onWindowResize() {
    if (containerEl) {
      containerWidth = containerEl.clientWidth;
      containerHeight = containerEl.clientHeight;
    }
  }

  function toggleSection(sectionId: string, collapsed: boolean) {
    collapsedOverride = { ...collapsedOverride, [sectionId]: !collapsed };
  }

  function widgetStyle(widget: DashboardWidget, region: LayoutRegion): string {
    if (!gridConfig) return '';
    const pos = widgetRect(
      { x: widget.x, y: widget.y, width: widget.width, height: widget.height },
      gridConfig,
      region.contentTop
    );
    return `position:absolute;left:${pos.left}px;top:${pos.top}px;width:${pos.width}px;height:${pos.height}px;`;
  }

  function sectionHeaderStyle(region: LayoutRegion): string {
    const padding = dashboard?.padding ?? 8;
    return `position:absolute;top:${region.headerTop}px;left:${padding}px;right:${padding}px;height:${region.headerHeight}px;`;
  }

  function widgetConfig(widget: DashboardWidget): string {
    try {
      const parsed = substituteConfig(JSON.parse(widget.config || '{}'), variableDefs, variableValues);
      parsed.dashboardWidgetId = widget.id;
      parsed.dashboardId = dashboard?.id || '';
      // The names the stored config refers to, which substitution hides:
      // a chart binds its zoom to the time range it names and refetches
      // only for the variables its queries use.
      const refs = [...referencesOf(widget)];
      if (refs.length) parsed.dashboardReferences = refs;
      return JSON.stringify(parsed);
    } catch {
      return JSON.stringify({ dashboardWidgetId: widget.id, dashboardId: dashboard?.id || '' });
    }
  }

  function presenceOf(widget: DashboardWidget, collapse = false) {
    return {
      visible: visible(widget),
      animateIn: widget.display?.animate_in,
      animateOut: widget.display?.animate_out,
      duration: widget.display?.duration,
      collapse,
    };
  }
</script>

<svelte:window
  onresize={onWindowResize}
  onpointerdown={(e) => {
    if (menuOpen && !(e.target as Element).closest?.('.variable-menu')) menuOpen = false;
    onInput();
  }}
  onkeydown={onInput}
  onwheel={onInput}
/>

{#snippet widgetCell(widget: DashboardWidget, style: string, collapse: boolean)}
  {@const cue = cueOf(widget)}
  <div
    class="widget-cell"
    class:transparent-tile={isTransparent(widget.display?.style)}
    data-dashboard-widget={widget.id}
    data-widget-package={widget.widget?.package_name || ''}
    style="{style}padding:{dashboard?.widget_padding ?? 4}px;{tileStyleCSS(widget.display?.style)}"
    use:presence={presenceOf(widget, collapse)}
  >
    <WidgetRenderer
      packageName={widget.widget?.package_name || ''}
      tagName={widget.widget?.tag_name || ''}
      entryPoint={widget.widget?.entry_point || 'index.js'}
      config={widgetConfig(widget)}
    />
    {#if cue}<TileCue {cue} still={rendering} />{/if}
  </div>
{/snippet}

{#snippet picker(variable: import('../lib/stores/dashboards').DashboardVariable, showLabel: boolean)}
  {#if variable.type === 'time_range'}
    <span class="variable-picker" title={variable.description || undefined}>
      {#if showLabel}<span>{variable.label || variable.name}</span>{/if}
      <TimeRangePicker
        value={variableValues[variable.name] ?? defaultValue(variable)}
        presets={optionsOf(variable)}
        label={variable.label || variable.name}
        onpick={(v) => pickVariable(variable.name, v)}
      />
    </span>
  {:else}
  <label class="variable-picker" title={variable.description || undefined}>
    {#if showLabel}<span>{variable.label || variable.name}</span>{/if}
    <select
      class="input"
      aria-label={variable.label || variable.name}
      value={variableValues[variable.name]}
      onchange={(e) => pickVariable(variable.name, (e.currentTarget as HTMLSelectElement).value)}
    >
      {#each optionsOf(variable) as option, i (i)}
        <option value={option.value}>{optionText(option)}</option>
      {/each}
    </select>
  </label>
  {/if}
{/snippet}

{#snippet regionCell(widget: DashboardWidget, _region: RegionId)}
  {@render widgetCell(widget, 'width:100%;height:100%;', true)}
{/snippet}

<!-- Fills the shell rather than the viewport: the shell is already pinned to
     100dvh, and 100vh overshoots it on a mobile browser with retracting chrome. -->
<div class="flex flex-col h-full">
  {#if loading}
    <div class="flex items-center justify-center flex-1 text-text-muted">
      Loading dashboard...
    </div>
  {:else if !dashboard}
    <div class="flex items-center justify-center flex-1 text-text-muted">
      Dashboard not found.
    </div>
  {:else}
    {#if barPickers.length > 0}
      <div class="variable-bar">
        {#each barPickers as variable (variable.name)}
          {@render picker(variable, displayOf(variable) === 'above')}
        {/each}
      </div>
    {/if}
    <div
      bind:this={containerEl}
      bind:clientWidth={containerWidth}
      bind:clientHeight={containerHeight}
      class="flex-1 relative"
      class:overflow-y-auto={scrolling}
      class:overflow-hidden={!scrolling}
      style="background-color: var(--theme-base);"
    >
      <!-- The inner surface is what the widgets are positioned against: in the
           scrolling layout it is as tall as the layout needs and the container
           scrolls over it. -->
      <div
        class="relative surface"
        style="{scrolling && layout ? `height:${layout.totalHeight}px;` : 'height:100%;'}transform:translate({shift.x}px,{shift.y}px);"
      >
        {#if regionMode}
          <RegionLayout
            widgets={dashboard.widgets ?? []}
            gridCols={dashboard.grid_cols}
            rowHeight={dashboard.row_height || DEFAULT_ROW_HEIGHT}
            gap={dashboard.gap ?? 8}
            padding={dashboard.padding ?? 8}
            cell={regionCell}
          />
        {:else if layout && containerWidth > 0}
          {#each layout.regions as region (region.id)}
            {#if region.section}
              <div style={sectionHeaderStyle(region)}>
                <button
                  class="section-header"
                  onclick={() => toggleSection(region.section!.id, region.collapsed)}
                  aria-expanded={!region.collapsed}
                >
                  <i class="las {region.collapsed ? 'la-angle-right' : 'la-angle-down'}"></i>
                  <span class="section-title">{region.section.title}</span>
                  {#if region.collapsed}
                    {@const hidden = (dashboard.widgets ?? []).filter((w) => w.section_id === region.section!.id).length}
                    <span class="section-count">{hidden} widget{hidden === 1 ? '' : 's'}</span>
                  {/if}
                </button>
              </div>
            {/if}

            {#each region.widgets as widget (widget.id)}
              {@render widgetCell(widget, widgetStyle(widget, region), false)}
            {/each}
          {/each}
        {/if}
      </div>

      {#if menuPickers.length > 0}
        <div class="variable-menu">
          <button
            class="variable-menu-button"
            class:open={menuOpen}
            aria-label="Dashboard controls"
            aria-expanded={menuOpen}
            title="Dashboard controls"
            onclick={() => (menuOpen = !menuOpen)}
          >
            <i class="las la-sliders-h"></i>
          </button>
          {#if menuOpen}
            <div class="variable-menu-panel">
              {#each menuPickers as variable (variable.name)}
                {@render picker(variable, true)}
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      <DashboardAlerts alerts={live.alerts} />

      {#if dim > 0}
        <div
          class="dimmer"
          style="opacity:{dim / 100};"
          role="button"
          tabindex="-1"
          aria-label="Wake the screen"
          onpointerdown={wakeTap}
          onclick={wakeTap}
          onkeydown={wakeTap}
        ></div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .surface {
    transition: transform 1s ease;
  }

  .widget-cell {
    position: relative;
    display: flex;
    overflow: hidden;
    background: var(--theme-widget-bg, var(--theme-card-bg, var(--theme-surface0, #313244)));
    border: none;
    border-radius: 12px;
    box-shadow: var(--theme-shadow-sm, 0 1px 2px rgba(0, 0, 0, 0.05));
    transition: box-shadow 0.2s ease, transform 0.2s ease;
  }

  .widget-cell:hover {
    box-shadow: var(--theme-shadow-md, 0 4px 6px rgba(0, 0, 0, 0.1));
    transform: translateY(-1px);
  }

  /* A transparent tile has no face to lift. */
  .widget-cell.transparent-tile:hover {
    box-shadow: none;
    transform: none;
  }

  .variable-bar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    padding: 0.4rem 0.75rem;
    background-color: var(--theme-base);
    border-bottom: 1px solid var(--theme-surface0, #313244);
  }

  .variable-picker {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .variable-picker select {
    padding: 0.2rem 0.5rem;
    font-size: 0.8rem;
  }

  .variable-menu {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    z-index: 80;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.4rem;
  }

  .variable-menu-button {
    width: 2rem;
    height: 2rem;
    border-radius: 999px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: var(--theme-mantle, #181825);
    color: var(--theme-text, #cdd6f4);
    opacity: 0.35;
    cursor: pointer;
    transition: opacity 0.2s ease;
  }

  .variable-menu-button:hover,
  .variable-menu-button.open {
    opacity: 1;
  }

  .variable-menu-panel {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.6rem 0.75rem;
    border-radius: 8px;
    background: var(--theme-mantle, #181825);
    border: 1px solid var(--theme-surface1, #45475a);
    box-shadow: var(--theme-shadow-md, 0 4px 6px rgba(0, 0, 0, 0.3));
  }

  .variable-menu-panel .variable-picker {
    justify-content: space-between;
  }

  .dimmer {
    position: absolute;
    inset: 0;
    z-index: 90;
    background: #000;
    transition: opacity 1.5s ease;
  }

  .section-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    height: 100%;
    padding: 0 0.25rem;
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    font-size: 0.95rem;
    font-weight: 600;
    cursor: pointer;
    text-align: left;
  }

  .section-header:hover {
    color: var(--theme-accent, #89b4fa);
  }

  .section-title {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .section-count {
    font-size: 0.7rem;
    font-weight: 400;
    opacity: 0.6;
  }
</style>
