<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { fly, fade } from 'svelte/transition';
  import { navigate } from 'svelte-routing';
  import { dashboards } from '../lib/stores/dashboards';
  import { dashboardGroupsStore } from '../lib/stores/dashboardGroups';
  import { currentUser } from '../lib/stores/auth';
  import { widgetRegistry } from '../lib/stores/widgets';
  import { themes } from '../lib/stores/themes';
  import type { Theme } from '../lib/stores/themes';
  import { cellSize, widgetRect, pixelsToGrid, hasAnyCollision, isWithinBounds, findFreePosition, DEFAULT_ROW_HEIGHT } from '../lib/grid';
  import { computeRegions, regionAt, widgetsIn, SECTION_HEADER_HEIGHT } from '../lib/dashboardLayout';
  import type { LayoutRegion } from '../lib/dashboardLayout';
  import { slugify } from '../lib/slug';
  import { confirm } from '../lib/stores/confirmDialog';
  import WidgetRenderer from '../lib/components/WidgetRenderer.svelte';
  import WidgetConfigRenderer from '../lib/components/WidgetConfigRenderer.svelte';
  import Modal from '../lib/components/Modal.svelte';
  import EditorAssistant from '../lib/components/EditorAssistant.svelte';
  import PlacementDisplayPanel from '../lib/components/PlacementDisplayPanel.svelte';
  import RegionLayout from '../lib/components/RegionLayout.svelte';
  import { activeCue, orderedPages } from '../lib/displayState';
  import type { PrimaryAttributes } from '../lib/displayState';
  import { watchConditionSources } from '../lib/dashboardLive';
  import { substituteCondition } from '../lib/variables';
  import { isTransparent, tileStyleCSS } from '../lib/tileStyle';
  import TileCue from '../lib/components/TileCue.svelte';
  import { regionOf as placementRegion, DEFAULT_REGION, type RegionId } from '../lib/regionLayout';
  import { watchDocument } from '../lib/stores/documentSync';
  import FormField from '../lib/components/FormField.svelte';
  import { relTime } from '../lib/utils/time';
  import type { Dashboard, DashboardDisplay, DashboardVariable, DashboardPage, DashboardSection, DashboardVersion, DashboardWidget, Widget, WidgetDisplay } from '../lib/stores/dashboards';
  import { defaultValue, loadVariables, optionOf, optionsOf, optionText, substituteConfig } from '../lib/variables';
  import VariablesEditor, { draftOf, variableOf } from '../lib/components/VariablesEditor.svelte';
  import { publishVariables } from '../lib/widgetBridge';
  import type { VariableDraft } from '../lib/components/VariablesEditor.svelte';
  import type { VariableValues } from '../lib/variables';
  import type { DashboardGroup } from '../lib/stores/dashboardGroups';
  import { api } from '../lib/api/client';
  import { setDashboardContext } from '../lib/dashboardContext';
  import type { ApiError } from '../lib/api/client';
  import type { GridPosition } from '../lib/grid';

  let { id }: { id: string } = $props();

  let dashboard = $state<Dashboard | null>(null);
  let containerEl = $state<HTMLDivElement | undefined>(undefined);
  let containerWidth = $state(0);
  let containerHeight = $state(0);
  let loading = $state(true);
  let saving = $state(false);
  let showWidgetPalette = $state(false);
  let selectedWidget = $state<DashboardWidget | null>(null);
  let showSettings = $state(false);
  let settingsName = $state('');
  let settingsSlug = $state('');
  let settingsSlugAutoGen = $state(false);
  let settingsDescription = $state('');
  let settingsLayoutMode = $state<'fit' | 'scroll' | 'regions'>('fit');
  let settingsPageInterval = $state(0);
  let settingsNightEnabled = $state(false);
  let settingsNightFrom = $state('23:00');
  let settingsNightTo = $state('07:00');
  let settingsNightLevel = $state(80);
  let settingsIdleEnabled = $state(false);
  let settingsIdleAfter = $state(300);
  let settingsIdleLevel = $state(100);
  let settingsPixelShift = $state(0);
  // Variables are edited as drafts; VariablesEditor owns their shape.
  let settingsVariables = $state<VariableDraft[]>([]);

  // The settings dialog shows one tab at a time; inactive tabs are not
  // rendered, so their values live in the settings state above.
  type SettingsTab = 'general' | 'layout' | 'screen' | 'variables' | 'sharing';
  const SETTINGS_TABS: { id: SettingsTab; label: string; icon: string }[] = [
    { id: 'general', label: 'General', icon: 'la-info-circle' },
    { id: 'layout', label: 'Layout', icon: 'la-th-large' },
    { id: 'screen', label: 'Screen', icon: 'la-desktop' },
    { id: 'variables', label: 'Variables', icon: 'la-dollar-sign' },
    { id: 'sharing', label: 'Sharing', icon: 'la-share-alt' },
  ];
  let settingsTab = $state<SettingsTab>('general');
  let settingsError = $state('');

  // settingsTabFor names the tab holding the field a server error is about,
  // from the field path the message starts with.
  function settingsTabFor(message: string): SettingsTab | null {
    if (message.startsWith('display.variables')) return 'variables';
    if (message.startsWith('display.')) return 'screen';
    if (/^(grid_|row_height|padding|gap|widget_padding|layout_mode)/.test(message)) return 'layout';
    return null;
  }

  // The canvas previews variables with values the author picks here. They are
  // the editor's own and never change what viewers see.
  let previewVariableValues = $state<VariableValues>({});
  let previewMenuOpen = $state(false);
  // The server resolves queries and constants for the draft, so the canvas
  // previews the same options viewers will be offered.
  let resolvedVariables = $state<DashboardVariable[] | null>(null);
  let followedVariables = $state<Record<string, string>>({});
  let storedVariablesKey = $derived(JSON.stringify(dashboard?.display?.variables ?? []));
  $effect(() => {
    const key = storedVariablesKey;
    const current = dashboard;
    if (!current || key === '[]') {
      resolvedVariables = null;
      return;
    }
    let stale = false;
    void loadVariables(current.id, true, current.display?.variables ?? []).then((r) => {
      if (stale) return;
      resolvedVariables = r.variables;
      followedVariables = r.followed;
    });
    return () => { stale = true; };
  });
  let variableDefs = $derived<DashboardVariable[]>(resolvedVariables ?? dashboard?.display?.variables ?? []);
  let previewPickers = $derived(variableDefs.filter((v) => v.type !== 'constant'));
  let previewValues = $derived<VariableValues>(Object.fromEntries(
    variableDefs.map((f) => [
      f.name,
      optionOf(f, previewVariableValues[f.name])?.value ?? optionOf(f, followedVariables[f.name])?.value ?? defaultValue(f),
    ])
  ));

  // The canvas and settings panels read variables through the bridge; a
  // widget that sets one, such as Variable buttons, changes the preview.
  $effect(() => {
    publishVariables({
      dashboardId: dashboard?.id ?? '',
      variables: variableDefs,
      values: previewValues,
      setValue: (name, value) => (previewVariableValues = { ...previewVariableValues, [name]: value }),
    });
  });
  onDestroy(() => publishVariables({ dashboardId: '', variables: [], values: {} }));

  // canvasConfig is what a widget on the canvas receives: its stored config
  // with the previewed variable values substituted and the host IDs stamped.
  function canvasConfig(widget: DashboardWidget): string {
    try {
      const parsed = substituteConfig(JSON.parse(widget.config || '{}'), variableDefs, previewValues);
      parsed.dashboardWidgetId = widget.id;
      parsed.dashboardId = dashboard?.id || '';
      return JSON.stringify(parsed);
    } catch {
      return JSON.stringify({ dashboardWidgetId: widget.id, dashboardId: dashboard?.id || '' });
    }
  }

  // ---- Status cues on the canvas ----
  //
  // The canvas shows every placement whatever its visibility conditions, but
  // draws cues live, so an author sees the border a viewer would.
  let cueRecords = $state<Record<string, unknown>>({});
  let cueNow = $state(new Date());
  let cuePrimary = $state<PrimaryAttributes>({});
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
      cuePrimary = next;
    })
    .catch(() => {});
  let cueConditions = $derived(
    (dashboard?.widgets ?? [])
      .flatMap((w) => (w.display?.cues ?? []).flatMap((c) => c.conditions ?? []))
      .filter((c) => c.kind === 'state')
      .map((c) => substituteCondition(c, variableDefs, previewValues))
  );
  let cueKey = $derived(JSON.stringify(cueConditions));
  $effect(() => {
    const key = cueKey;
    if (!key || cueConditions.length === 0) return;
    return watchConditionSources(cueConditions, (next) => (cueRecords = next));
  });
  const cueClock = setInterval(() => (cueNow = new Date()), 15000);
  onDestroy(() => clearInterval(cueClock));

  function canvasCue(widget: DashboardWidget) {
    return activeCue(widget.display, cueRecords, cueNow, { variables: variableDefs, values: previewValues }, cuePrimary);
  }

  let settingsGridRows = $state(12);
  let settingsGridCols = $state(12);
  let settingsRowHeight = $state(DEFAULT_ROW_HEIGHT);
  let settingsPadding = $state(8);
  let settingsGap = $state(8);
  let settingsWidgetPadding = $state(4);
  let settingsIsPublic = $state(false);
  let settingsPublicControl = $state(false);
  let settingsGroupId = $state<number | null>(null);
  let settingsSlugError = $state('');
  let dashboardGroupsList: DashboardGroup[] = $state([]);
  let showThemeSelector = $state(false);
  let selectedThemeId = $state<number | null>(null);
  let originalThemeId = $state<number | null>(null);
  let savingTheme = $state(false);
  let themeList: Theme[] = $state([]);

  // Drag-and-drop state.
  let dragging = $state<DashboardWidget | null>(null);
  let dragOffsetX = $state(0);
  let dragOffsetY = $state(0);
  let dragCurrentLeft = $state(0);
  let dragCurrentTop = $state(0);
  let dragSnappedPos = $state<{ x: number; y: number } | null>(null);
  let dragValid = $state(false);
  let didDrag = $state(false);
  let dragStartX = $state(0);
  let dragStartY = $state(0);
  // The region the pointer is currently over, which is the section a drop
  // lands in. Its ID, not the object, so it survives a layout recompute.
  let dragRegionId = $state('');

  // Resize state.
  let resizing = $state<DashboardWidget | null>(null);
  let resizeSnappedSize = $state<{ width: number; height: number } | null>(null);
  let resizeValid = $state(false);
  let didResize = $state(false);
  let resizeStartX = $state(0);
  let resizeStartY = $state(0);

  let scrolling = $derived(dashboard?.layout_mode === 'scroll');
  let regionMode = $derived(dashboard?.layout_mode === 'regions');

  // Pages filter the canvas. '' shows every placement; a page shows its own
  // placements and those on every page, which is what a viewer of it sees.
  let pages = $derived(orderedPages(dashboard?.pages));
  let pageFilter = $state('');
  let canvasWidgets = $derived((dashboard?.widgets ?? []).filter(
    (w) => !pageFilter || !w.page_id || w.page_id === pageFilter
  ));
  // The region a new widget lands in under the region layout.
  let targetRegion = $state<RegionId>(DEFAULT_REGION);

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

  // Empty regions keep two rows of height while editing so a new section is
  // something a widget can actually be dragged into.
  let layout = $derived(gridConfig && dashboard
    ? computeRegions(canvasWidgets, dashboard.sections ?? [], gridConfig, { minRows: 2 })
    : null);
  let regions = $derived(layout?.regions ?? []);

  function regionOf(widget: DashboardWidget): LayoutRegion | undefined {
    const id = widget.section_id ?? '';
    return regions.find((r) => r.id === id);
  }

  let availableWidgets = $derived($widgetRegistry.widgets);

  // Auto-generate settings slug from name when auto-gen is active.
  $effect(() => {
    if (settingsSlugAutoGen && showSettings) {
      settingsSlug = slugify(settingsName);
    }
  });

  onMount(async () => {
    const [dash] = await Promise.all([
      dashboards.get(id, 'draft'),
      widgetRegistry.list(),
      themes.list(),
      dashboardGroupsStore.list().then((g) => { dashboardGroupsList = g; }).catch(() => {}),
    ]);
    dashboard = dash;
    // The editor renders live widgets against the draft, so it carries the
    // same claim the viewer does. An author's own draft grants them too.
    setDashboardContext(dash?.id ?? null);
    draftChanged = dash?.has_draft_changes ?? false;
    if (dash?.theme_id) {
      themes.applyById(dash.theme_id);
    }
    loading = false;
  });

  // The draft persists on every edit, so "changed" is not about saving: it
  // tracks whether the draft has drifted from the version viewers are served.
  // The server answers that on load and after a deploy; between them, every
  // mutation here is a drift.
  let draftChanged = $state(false);

  let disposed = false;
  let refreshGeneration = 0;
  onDestroy(() => { disposed = true; });
  let sharedDocumentID = $derived(dashboard?.id);
  $effect(() => {
    const documentID = sharedDocumentID;
    if (!documentID) return;
    return watchDocument('dashboard', documentID, async () => {
      if (loading || dragging || resizing) return false;
      await refreshAssistantDraft();
      return true;
    }, (error: any) => showToast(error?.message || 'could not refresh the dashboard'));
  });

  async function refreshAssistantDraft() {
    const generation = ++refreshGeneration;
    const updated = await dashboards.get(id, 'draft');
    if (!updated) throw new Error('could not refresh the dashboard');
    if (disposed || generation !== refreshGeneration) return;
    dashboard = updated;
    // Revisions remembered from this editor's own saves stay; a widget
    // changed elsewhere keeps the revision it had here, so the next save of
    // it is refused and reloaded rather than silently overwriting.
    draftChanged = updated.has_draft_changes ?? false;
    if (selectedWidget) selectedWidget = updated.widgets?.find(w => w.id === selectedWidget?.id) ?? null;
    themes.applyById(updated.theme_id ?? null);
  }

  function markDraftChanged() {
    draftChanged = true;
  }

  onDestroy(() => {
    setDashboardContext(null);
    themes.applyById(null);
  });

  let ownedGroups = $derived(dashboardGroupsList.filter((g) => g.owner_id === $currentUser?.id));

  function onWindowResize() {
    if (containerEl) {
      containerWidth = containerEl.clientWidth;
      containerHeight = containerEl.clientHeight;
    }
  }

  function widgetStyle(widget: DashboardWidget): string {
    if (!gridConfig) return '';
    const region = regionOf(widget);
    if (!region) return 'display:none;';
    const pos = widgetRect(
      { x: widget.x, y: widget.y, width: widget.width, height: widget.height },
      gridConfig,
      region.contentTop
    );
    return `position:absolute;left:${pos.left}px;top:${pos.top}px;width:${pos.width}px;height:${pos.height}px;`;
  }

  async function addWidget(registryWidget: Widget) {
    if (!dashboard || !gridConfig) return;

    const initialConfig: Record<string, any> = {};
    if (registryWidget.default_provider) {
      initialConfig.provider = registryWidget.default_provider;
    }
    const defaultConfig = JSON.stringify(initialConfig);
    const width = 3;
    const height = 3;

    // The region layout stacks placements, so a new one goes to the bottom
    // of the chosen region at full width rather than into a free cell.
    if (regionMode) {
      const inRegion = (dashboard.widgets ?? []).filter((w) => placementRegion(w) === targetRegion);
      const y = inRegion.reduce((max, w) => Math.max(max, w.y + 1), 0);
      try {
        const placed = await dashboards.addWidget(dashboard.id, {
          widget_id: registryWidget.id,
          x: 0,
          y,
          width: dashboard.grid_cols,
          height: 2,
          region: targetRegion,
          page_id: pageFilter || undefined,
          config: defaultConfig,
        });
        dashboard.widgets = [...(dashboard.widgets || []), placed];
        markDraftChanged();
        showWidgetPalette = false;
      } catch (e: any) {
        showToast(e?.message || 'Failed to add the widget');
      }
      return;
    }

    // A new widget lands in the last open region: on a scrolling dashboard
    // that is the bottom of the page, which is where "add another" belongs.
    const open = regions.filter((r) => !r.collapsed);
    const target = scrolling ? open[open.length - 1] : regions[0];
    const targetId = target?.id ?? '';
    const occupied: GridPosition[] = widgetsIn(canvasWidgets, targetId).map((w) => ({
      x: w.x, y: w.y, width: w.width, height: w.height,
    }));

    const pos = findFreePosition(width, height, occupied, gridConfig);
    if (!pos) {
      showToast('No free space available on the grid for this widget.');
      return;
    }

    try {
      const placed = await dashboards.addWidget(dashboard.id, {
        widget_id: registryWidget.id,
        section_id: targetId || undefined,
        x: pos.x,
        y: pos.y,
        width: pos.width,
        height: pos.height,
        page_id: pageFilter || undefined,
        config: defaultConfig,
      });
      dashboard.widgets = [...(dashboard.widgets || []), placed];
      markDraftChanged();
      showWidgetPalette = false;
    } catch (e) {
      console.error('Failed to add widget', e);
    }
  }

  function selectWidget(widget: DashboardWidget) {
    selectedWidget = widget;
    // The panel shows the config as of this revision; a save names it.
    if (!configSaving.has(widget.id)) configRevision.set(widget.id, widget.config_revision ?? 0);
  }

  // Config panels save on every keystroke. Saves are serialized per widget and
  // coalesced to the latest value, so two requests for one widget cannot land
  // out of order and leave the server holding an older config than the panel
  // shows.
  const configSaving = new Map<string, Promise<void>>();
  const configPending = new Map<string, string>();
  // configRevision is the config revision this editor last read or wrote for
  // each placement. A save names it, so a config another editor or an agent
  // replaced meanwhile is reloaded rather than overwritten.
  const configRevision = new Map<string, number>();
  // panelEpoch remounts the settings panel after a conflict, because a panel
  // seeds from its config once per placement.
  let panelEpoch = $state(0);

  function saveWidgetConfig(dashboardId: string, widgetId: string, config: string): Promise<void> {
    configPending.set(widgetId, config);
    const running = configSaving.get(widgetId);
    if (running) return running;
    const chain = (async () => {
      try {
        while (configPending.has(widgetId)) {
          const next = configPending.get(widgetId)!;
          configPending.delete(widgetId);
          const known = configRevision.get(widgetId) ?? 0;
          const saved = await dashboards.updateWidget(dashboardId, widgetId, { config: next, expected_config_revision: known });
          if (saved?.config_revision !== undefined) configRevision.set(widgetId, saved.config_revision);
        }
      } finally {
        configSaving.delete(widgetId);
      }
    })();
    configSaving.set(widgetId, chain);
    return chain;
  }

  async function handleCustomConfigChange(detail: any) {
    if (!dashboard || !selectedWidget) return;
    // The author may select another widget while this save is in flight, so
    // everything after the await names the widget the change was made on.
    const widget = selectedWidget;
    const cleanDetail = { ...detail };
    // Strip the injected IDs so they are not persisted in the config.
    delete cleanDetail.dashboardWidgetId;
    delete cleanDetail.dashboardId;
    const updatedConfig = JSON.stringify(cleanDetail);
    try {
      await saveWidgetConfig(dashboard.id, widget.id, updatedConfig);
      dashboard.widgets = dashboard.widgets?.map((w) =>
        w.id === widget.id ? { ...w, config: updatedConfig } : w
      );
      if (selectedWidget?.id === widget.id) {
        selectedWidget = { ...selectedWidget, config: updatedConfig };
      }
      markDraftChanged();
    } catch (e: any) {
      if (e?.code === 'CONFLICT') {
        // Someone else replaced this widget's settings. Show theirs instead of
        // overwriting them; the change made here has to be applied again.
        configPending.delete(widget.id);
        await refreshAssistantDraft().catch(() => {});
        const fresh = dashboard?.widgets?.find((w) => w.id === widget.id);
        configRevision.set(widget.id, fresh?.config_revision ?? 0);
        panelEpoch++;
        showToast('These settings were changed elsewhere and have been reloaded. Apply your change again.');
        return;
      }
      console.error('Failed to save widget config', e);
    }
  }

  async function removeWidget(widgetId: string) {
    if (!dashboard) return;
    if (!await confirm('Remove this widget?')) return;
    await dashboards.removeWidget(dashboard.id, widgetId);
    dashboard.widgets = dashboard.widgets?.filter((w) => w.id !== widgetId);
    markDraftChanged();
    if (selectedWidget?.id === widgetId) selectedWidget = null;
  }

  // --- Sections ---

  // Sections are the scrolling layout's bands. Every mutation here writes
  // through immediately, the same as a widget move: the live rows are the
  // draft, and Deploy is what reaches viewers.

  async function addSection() {
    if (!dashboard) return;
    try {
      const section = await dashboards.addSection(dashboard.id, 'New section');
      dashboard.sections = [...(dashboard.sections ?? []), section];
      markDraftChanged();
    } catch (e: any) {
      showToast(e?.message || 'Failed to add the section');
    }
  }

  async function patchSection(section: DashboardSection, data: Partial<DashboardSection>) {
    if (!dashboard) return;
    const previous = { ...section };
    dashboard.sections = (dashboard.sections ?? []).map((s) =>
      s.id === section.id ? { ...s, ...data } : s
    );
    markDraftChanged();
    try {
      await dashboards.updateSection(dashboard.id, section.id, data);
    } catch (e: any) {
      dashboard.sections = (dashboard.sections ?? []).map((s) =>
        s.id === section.id ? previous : s
      );
      showToast(e?.message || 'Failed to update the section');
    }
  }

  async function removeSection(section: DashboardSection) {
    if (!dashboard) return;
    const count = (dashboard.widgets ?? []).filter((w) => w.section_id === section.id).length;
    const detail = count === 0
      ? ''
      : ` The ${count} widget${count === 1 ? '' : 's'} in it will be removed as well.`;
    if (!await confirm(`Remove the section "${section.title}"?${detail}`)) return;
    try {
      await dashboards.removeSection(dashboard.id, section.id);
      dashboard.sections = (dashboard.sections ?? []).filter((s) => s.id !== section.id);
      dashboard.widgets = (dashboard.widgets ?? []).filter((w) => w.section_id !== section.id);
      if (selectedWidget?.section_id === section.id) selectedWidget = null;
      markDraftChanged();
    } catch (e: any) {
      showToast(e?.message || 'Failed to remove the section');
    }
  }

  // moveSection swaps a section with its neighbour and renumbers the whole
  // list, so the stored order never depends on which pair last moved.
  async function moveSection(section: DashboardSection, delta: number) {
    if (!dashboard) return;
    const ordered = [...(dashboard.sections ?? [])].sort((a, b) => a.sort_order - b.sort_order);
    const from = ordered.findIndex((s) => s.id === section.id);
    const to = from + delta;
    if (from < 0 || to < 0 || to >= ordered.length) return;
    const previous = dashboard.sections;
    ordered.splice(to, 0, ...ordered.splice(from, 1));
    const renumbered = ordered.map((s, i) => ({ ...s, sort_order: i }));
    dashboard.sections = renumbered;
    markDraftChanged();
    try {
      await dashboards.reorderSections(
        dashboard.id,
        renumbered.map((s) => ({ id: s.id, sort_order: s.sort_order }))
      );
    } catch (e: any) {
      dashboard.sections = previous;
      showToast(e?.message || 'Failed to reorder the sections');
    }
  }

  // --- Pages ---

  // Pages write through like sections do. Deleting one moves its placements
  // to every page on the server, so the editor mirrors that locally.

  let renamingPage = $state('');

  async function addPage() {
    if (!dashboard) return;
    try {
      const page = await dashboards.addPage(dashboard.id, `Page ${pages.length + 1}`);
      dashboard.pages = [...(dashboard.pages ?? []), page];
      pageFilter = page.id;
      renamingPage = page.id;
      markDraftChanged();
    } catch (e: any) {
      showToast(e?.message || 'Failed to add the page');
    }
  }

  async function renamePage(page: DashboardPage, title: string) {
    renamingPage = '';
    if (!dashboard || !title.trim() || title.trim() === page.title) return;
    try {
      const updated = await dashboards.updatePage(dashboard.id, page.id, { title: title.trim() });
      dashboard.pages = (dashboard.pages ?? []).map((p) => (p.id === page.id ? updated : p));
      markDraftChanged();
    } catch (e: any) {
      showToast(e?.message || 'Failed to rename the page');
    }
  }

  async function movePage(page: DashboardPage, delta: number) {
    if (!dashboard) return;
    const ordered = [...pages];
    const from = ordered.findIndex((p) => p.id === page.id);
    const to = from + delta;
    if (from < 0 || to < 0 || to >= ordered.length) return;
    ordered.splice(to, 0, ...ordered.splice(from, 1));
    const renumbered = ordered.map((p, i) => ({ ...p, sort_order: i }));
    const previous = dashboard.pages;
    dashboard.pages = renumbered;
    markDraftChanged();
    try {
      await dashboards.reorderPages(dashboard.id, renumbered.map((p) => ({ id: p.id, sort_order: p.sort_order })));
    } catch (e: any) {
      dashboard.pages = previous;
      showToast(e?.message || 'Failed to reorder the pages');
    }
  }

  async function removePage(page: DashboardPage) {
    if (!dashboard) return;
    const count = (dashboard.widgets ?? []).filter((w) => w.page_id === page.id).length;
    const detail = count === 0 ? '' : ` Its ${count} widget${count === 1 ? '' : 's'} will move to every page.`;
    if (!await confirm(`Remove the page "${page.title}"?${detail}`)) return;
    try {
      await dashboards.removePage(dashboard.id, page.id);
      dashboard.pages = (dashboard.pages ?? []).filter((p) => p.id !== page.id);
      dashboard.widgets = (dashboard.widgets ?? []).map((w) => (w.page_id === page.id ? { ...w, page_id: null } : w));
      if (pageFilter === page.id) pageFilter = '';
      markDraftChanged();
    } catch (e: any) {
      showToast(e?.message || 'Failed to remove the page');
    }
  }

  // --- Placement display settings ---

  // patchPlacement writes the host-owned fields of a placement: its page,
  // region, and display settings. The change shows at once and is rolled
  // back if the server refuses it.
  async function patchPlacement(widget: DashboardWidget, patch: { page_id?: string; region?: string; display?: WidgetDisplay; y?: number }) {
    if (!dashboard) return;
    const previous = dashboard.widgets;
    const local: Partial<DashboardWidget> = { ...patch };
    if (patch.page_id !== undefined) local.page_id = patch.page_id || null;
    dashboard.widgets = (dashboard.widgets ?? []).map((w) => (w.id === widget.id ? { ...w, ...local } : w));
    if (selectedWidget?.id === widget.id) selectedWidget = { ...selectedWidget, ...local };
    markDraftChanged();
    try {
      await dashboards.updateWidget(dashboard.id, widget.id, patch as Partial<DashboardWidget>);
    } catch (e: any) {
      dashboard.widgets = previous;
      if (selectedWidget?.id === widget.id) selectedWidget = previous?.find((w) => w.id === widget.id) ?? null;
      showToast(e?.message || 'Failed to save the display settings');
    }
  }

  // moveInRegion swaps a placement with its neighbour in its region and
  // renumbers the region, so the stored order never depends on which pair
  // last moved.
  async function moveInRegion(widget: DashboardWidget, delta: number) {
    if (!dashboard) return;
    const region = placementRegion(widget);
    const ordered = (dashboard.widgets ?? [])
      .filter((w) => placementRegion(w) === region)
      .sort((a, b) => a.y - b.y || a.x - b.x);
    const from = ordered.findIndex((w) => w.id === widget.id);
    const to = from + delta;
    if (from < 0 || to < 0 || to >= ordered.length) return;
    ordered.splice(to, 0, ...ordered.splice(from, 1));
    for (const [i, w] of ordered.entries()) {
      if (w.y !== i) await patchPlacement(w, { y: i });
    }
  }

  // changeRegion moves a placement to the bottom of another region.
  async function changeRegion(widget: DashboardWidget, region: string) {
    if (!dashboard) return;
    const y = (dashboard.widgets ?? [])
      .filter((w) => w.id !== widget.id && placementRegion(w) === region)
      .reduce((max, w) => Math.max(max, w.y + 1), 0);
    targetRegion = region as RegionId;
    await patchPlacement(widget, { region, y });
  }

  function openSettings() {
    if (!dashboard) return;
    previewMenuOpen = false;
    settingsName = dashboard.name;
    settingsSlug = dashboard.slug;
    settingsSlugAutoGen = false;
    settingsDescription = dashboard.description;
    settingsLayoutMode = dashboard.layout_mode === 'scroll' || dashboard.layout_mode === 'regions' ? dashboard.layout_mode : 'fit';
    const display = dashboard.display ?? {};
    settingsPageInterval = display.page_interval ?? 0;
    settingsNightEnabled = !!display.night_dim;
    settingsNightFrom = display.night_dim?.from ?? '23:00';
    settingsNightTo = display.night_dim?.to ?? '07:00';
    settingsNightLevel = display.night_dim?.level ?? 80;
    settingsIdleEnabled = !!display.idle_dim;
    settingsIdleAfter = display.idle_dim?.after ?? 300;
    settingsIdleLevel = display.idle_dim?.level ?? 100;
    settingsPixelShift = display.pixel_shift ?? 0;
    settingsVariables = (display.variables ?? []).map(draftOf);
    settingsGridRows = dashboard.grid_rows;
    settingsGridCols = dashboard.grid_cols;
    settingsRowHeight = dashboard.row_height || DEFAULT_ROW_HEIGHT;
    settingsPadding = dashboard.padding ?? 8;
    settingsGap = dashboard.gap ?? 8;
    settingsWidgetPadding = dashboard.widget_padding ?? 4;
    settingsIsPublic = dashboard.is_public;
    settingsPublicControl = dashboard.public_control;
    settingsGroupId = dashboard.group_id;
    settingsSlugError = '';
    settingsError = '';
    settingsTab = 'general';
    showSettings = true;
  }

  async function saveSettings() {
    if (!dashboard) return;
    settingsError = '';
    if (!settingsName.trim()) {
      settingsTab = 'general';
      settingsError = 'The dashboard needs a name.';
      return;
    }

    // Leaving the scrolling layout merges the sections into the sectionless
    // region, which is a one-way change to the layout: say so before it lands.
    const sectionCount = dashboard.sections?.length ?? 0;
    if (settingsLayoutMode === 'fit' && dashboard.layout_mode === 'scroll' && sectionCount > 0) {
      const ok = await confirm(
        `The fit layout has no sections. The ${sectionCount} section${sectionCount === 1 ? '' : 's'} will be removed and their widgets stacked into one grid. Continue?`
      );
      if (!ok) return;
    }

    saving = true;
    settingsSlugError = '';
    // An empty object clears the stored settings on the server.
    const display: DashboardDisplay = {};
    if (settingsPageInterval > 0) display.page_interval = settingsPageInterval;
    if (settingsNightEnabled) display.night_dim = { from: settingsNightFrom, to: settingsNightTo, level: settingsNightLevel };
    if (settingsIdleEnabled) display.idle_dim = { after: settingsIdleAfter, level: settingsIdleLevel };
    if (settingsPixelShift > 0) display.pixel_shift = settingsPixelShift;
    const variables = settingsVariables.map(variableOf);
    if (variables.length > 0) display.variables = variables;
    try {
      const payload: Record<string, any> = {
        display,
        expected_display_revision: dashboard.display_revision ?? 0,
        name: settingsName,
        slug: settingsSlug,
        description: settingsDescription,
        layout_mode: settingsLayoutMode,
        grid_rows: settingsGridRows,
        grid_cols: settingsGridCols,
        row_height: settingsRowHeight,
        padding: settingsPadding,
        gap: settingsGap,
        widget_padding: settingsWidgetPadding,
        is_public: settingsIsPublic,
        public_control: settingsPublicControl,
        group_id: settingsGroupId ?? 0,
      };
      // Only the layout geometry is versioned; a rename or a visibility change
      // reaches viewers without a deploy, so it must not mark the draft dirty.
      const geometryChanged =
        settingsLayoutMode !== dashboard.layout_mode ||
        settingsGridRows !== dashboard.grid_rows ||
        settingsGridCols !== dashboard.grid_cols ||
        settingsRowHeight !== (dashboard.row_height || DEFAULT_ROW_HEIGHT) ||
        settingsPadding !== (dashboard.padding ?? 8) ||
        settingsGap !== (dashboard.gap ?? 8) ||
        settingsWidgetPadding !== (dashboard.widget_padding ?? 4) ||
        JSON.stringify(display) !== JSON.stringify(dashboard.display ?? {});
      const flattened = settingsLayoutMode === 'fit' && dashboard.layout_mode === 'scroll';

      const updated = await dashboards.update(dashboard.id, payload);
      // The server rewrites placements when it flattens sections, so the
      // editor rereads rather than guessing where they landed.
      if (flattened) {
        const reloaded = await dashboards.get(updated.id, 'draft');
        if (reloaded) {
          dashboard = reloaded;
          draftChanged = reloaded.has_draft_changes ?? true;
          selectedWidget = null;
          showSettings = false;
          return;
        }
      }
      const widgets = dashboard.widgets;
      const sections = dashboard.sections;
      const pageRows = dashboard.pages;
      dashboard = { ...updated, widgets, sections, pages: pageRows };
      if (geometryChanged) markDraftChanged();
      showSettings = false;
    } catch (e) {
      const err = e as ApiError;
      if (err.code === 'CONFLICT' && /display settings changed/.test(err.message ?? '')) {
        // The display was replaced elsewhere since this dialog opened. Load
        // the current dashboard so a second Save knowingly replaces it.
        await refreshAssistantDraft().catch(() => {});
        settingsError = 'The display settings were changed elsewhere since this dialog opened. Save again to replace them with yours, or close to keep theirs.';
      } else if (err.code === 'CONFLICT') {
        settingsSlugError = err.message;
        settingsTab = 'general';
      } else {
        settingsError = err.message || 'The settings could not be saved.';
        settingsTab = settingsTabFor(settingsError) ?? settingsTab;
      }
    } finally {
      saving = false;
    }
  }

  // --- Deploy and version history ---

  let deploying = $state(false);
  let showHistory = $state(false);
  let historyLoading = $state(false);
  let historyVersions: DashboardVersion[] = $state([]);
  let restoring = $state(false);
  let toast = $state('');
  let toastTimer: ReturnType<typeof setTimeout> | null = null;

  function showToast(message: string) {
    toast = message;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = ''), 5000);
  }

  async function deploy() {
    if (!dashboard) return;
    deploying = true;
    try {
      const updated = await dashboards.deploy(dashboard.id);
      dashboard = { ...dashboard, deployed_version: updated.deployed_version };
      draftChanged = false;
      showToast(`Deployed version ${updated.deployed_version}`);
    } catch (e: any) {
      showToast(e?.message || 'Failed to deploy the dashboard');
    } finally {
      deploying = false;
    }
  }

  // exportDashboard downloads the draft layout as a portable JSON document.
  // Uploads and sharing stay behind, so the file is the layout, not the
  // dashboard's place in this install.
  async function exportDashboard() {
    if (!dashboard) return;
    try {
      const doc = await dashboards.exportDoc(dashboard.id);
      const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${dashboard.slug || 'dashboard'}.karakuri-dashboard.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e: any) {
      showToast(e?.message || 'Failed to export the dashboard');
    }
  }

  async function openHistory() {
    if (!dashboard) return;
    showHistory = true;
    historyLoading = true;
    try {
      historyVersions = (await dashboards.versions(dashboard.id)) ?? [];
    } catch (e: any) {
      showToast(e?.message || 'Failed to load version history');
      showHistory = false;
    } finally {
      historyLoading = false;
    }
  }

  // Restoring rewrites the draft in place; the editor reloads it rather than
  // patching each placement, since a restore can add and remove widgets.
  async function restoreVersion(version: number) {
    if (!dashboard) return;
    if (!await confirm(`Restore version ${version} into the draft? The current layout will be overwritten.`)) return;
    restoring = true;
    try {
      await dashboards.restoreVersion(dashboard.id, version);
      const reloaded = await dashboards.get(dashboard.id, 'draft');
      if (reloaded) {
        dashboard = reloaded;
        draftChanged = reloaded.has_draft_changes ?? false;
        themes.applyById(reloaded.theme_id ?? null);
      }
      selectedWidget = null;
      showHistory = false;
      showToast(`Restored version ${version} into the draft`);
    } catch (e: any) {
      showToast(e?.message || 'Failed to restore the version');
    } finally {
      restoring = false;
    }
  }

  async function openThemeSelector() {
    if (!dashboard) return;
    selectedThemeId = dashboard.theme_id ?? null;
    originalThemeId = selectedThemeId;
    await themes.list();
    themeList = $themes.themes;
    showThemeSelector = true;
  }

  function cancelThemeSelector() {
    themes.applyById(originalThemeId);
    showThemeSelector = false;
  }

  function selectThemePreview(themeId: number | null) {
    selectedThemeId = themeId;
    themes.applyById(themeId);
  }

  async function saveTheme() {
    if (!dashboard) return;
    savingTheme = true;
    try {
      const themeId = selectedThemeId != null ? Number(selectedThemeId) : 0;
      const updated = await api.put<Dashboard>(`/dashboards/${dashboard.id}`, { theme_id: themeId });
      dashboard = { ...dashboard, ...updated };
      showThemeSelector = false;
    } catch (e) {
      console.error('Failed to save theme', e);
    } finally {
      savingTheme = false;
    }
  }

  function themeCardStyle(theme: Theme): string {
    try {
      const vars: Record<string, string> = JSON.parse(theme.variables);
      const base = vars.base || '';
      const text = vars.text || '';
      const surface1 = vars.surface1 || '';
      const subtext0 = vars.subtext0 || '';
      const mauve = vars.mauve || vars.blue || '';
      const green = vars.green || vars.teal || '';
      return [
        base && `--card-base: ${base}`,
        text && `--card-text: ${text}`,
        surface1 && `--card-surface1: ${surface1}`,
        subtext0 && `--card-muted: ${subtext0}`,
        mauve && `--card-accent: ${mauve}`,
        green && `--card-success: ${green}`,
        base && `background-color: ${base}`,
        text && `color: ${text}`,
        surface1 && `border-color: ${surface1}`,
      ].filter(Boolean).join('; ');
    } catch {
      return '';
    }
  }

  function swatchColors(theme: Theme): string[] {
    try {
      const vars: Record<string, string> = JSON.parse(theme.variables);
      const keys = ['red', 'peach', 'yellow', 'green', 'teal', 'blue', 'mauve', 'pink'];
      return keys.map((k) => vars[k]).filter(Boolean);
    } catch {
      return [];
    }
  }

  // Drag-and-drop handlers. Pointer positions are measured against the
  // scrolling surface rather than the viewport, so a drag keeps tracking the
  // widget after the page scrolls under it.
  function pointerSurface(e: PointerEvent): { x: number; y: number } {
    if (!containerEl) return { x: 0, y: 0 };
    const rect = containerEl.getBoundingClientRect();
    return {
      x: e.clientX - rect.left,
      y: e.clientY - rect.top + containerEl.scrollTop,
    };
  }

  function onWidgetPointerDown(e: PointerEvent, widget: DashboardWidget) {
    if (!gridConfig || !containerEl) return;
    const region = regionOf(widget);
    if (!region) return;
    const pos = widgetRect(
      { x: widget.x, y: widget.y, width: widget.width, height: widget.height },
      gridConfig,
      region.contentTop
    );
    const p = pointerSurface(e);
    dragging = widget;
    dragOffsetX = p.x - pos.left;
    dragOffsetY = p.y - pos.top;
    dragCurrentLeft = pos.left;
    dragCurrentTop = pos.top;
    dragRegionId = region.id;
    dragSnappedPos = null;
    dragValid = false;
    didDrag = false;
    didResize = false;
    dragStartX = e.clientX;
    dragStartY = e.clientY;
    e.preventDefault();
  }

  function onPointerMove(e: PointerEvent) {
    if (!gridConfig || !dashboard || !containerEl) return;

    // Handle resize. A resize stays inside the widget's own region: growing a
    // widget past its section's last row simply makes the section taller.
    if (resizing) {
      const dx = e.clientX - resizeStartX;
      const dy = e.clientY - resizeStartY;
      if (Math.abs(dx) > 5 || Math.abs(dy) > 5) {
        didResize = true;
      }

      const region = regionOf(resizing);
      if (!region) return;
      const gap = gridConfig.gap ?? 8;
      const cell = cellSize(gridConfig);
      const widgetPx = widgetRect(
        { x: resizing.x, y: resizing.y, width: resizing.width, height: resizing.height },
        gridConfig,
        region.contentTop
      );
      const p = pointerSurface(e);
      const pixelW = p.x - widgetPx.left;
      const pixelH = p.y - widgetPx.top;
      const newWidth = Math.max(1, Math.round((pixelW + gap) / (cell.w + gap)));
      const newHeight = Math.max(1, Math.round((pixelH + gap) / (cell.h + gap)));

      resizeSnappedSize = { width: newWidth, height: newHeight };

      const candidate: GridPosition = {
        x: resizing.x, y: resizing.y, width: newWidth, height: newHeight,
      };
      const others: GridPosition[] = widgetsIn(canvasWidgets, region.id)
        .filter((w) => w.id !== resizing!.id)
        .map((w) => ({ x: w.x, y: w.y, width: w.width, height: w.height }));
      resizeValid = isWithinBounds(candidate, gridConfig) && !hasAnyCollision(candidate, others);
      return;
    }

    // Handle drag.
    if (!dragging) return;
    const p = pointerSurface(e);
    dragCurrentLeft = p.x - dragOffsetX;
    dragCurrentTop = p.y - dragOffsetY;

    const dx = e.clientX - dragStartX;
    const dy = e.clientY - dragStartY;
    if (Math.abs(dx) > 5 || Math.abs(dy) > 5) {
      didDrag = true;
    }

    // The region under the middle of the widget's first row is the one it
    // drops into; anchoring on the pointer instead would fight the snap.
    const cell = cellSize(gridConfig);
    const target = regionAt(regions, dragCurrentTop + cell.h / 2);
    dragRegionId = target?.id ?? '';

    const snapped = pixelsToGrid(
      { left: dragCurrentLeft, top: dragCurrentTop },
      gridConfig,
      target?.contentTop
    );
    dragSnappedPos = snapped;

    const candidate: GridPosition = {
      x: snapped.x, y: snapped.y,
      width: dragging.width, height: dragging.height,
    };
    const others: GridPosition[] = widgetsIn(canvasWidgets, dragRegionId)
      .filter((w) => w.id !== dragging!.id)
      .map((w) => ({ x: w.x, y: w.y, width: w.width, height: w.height }));

    dragValid = isWithinBounds(candidate, gridConfig) && !hasAnyCollision(candidate, others);
    autoScroll(e);
  }

  // autoScroll walks the page while a drag hovers near the edge of the
  // viewport, since a section further down the dashboard cannot be dropped
  // into if it cannot be reached.
  function autoScroll(e: PointerEvent) {
    if (!containerEl || !scrolling) return;
    const rect = containerEl.getBoundingClientRect();
    const edge = 48;
    const step = 12;
    if (e.clientY < rect.top + edge) {
      containerEl.scrollTop -= step;
    } else if (e.clientY > rect.bottom - edge) {
      containerEl.scrollTop += step;
    }
  }

  async function onPointerUp(e: PointerEvent) {
    // Handle resize release.
    if (resizing && dashboard && gridConfig) {
      const widget = resizing;
      if (didResize && resizeValid && resizeSnappedSize &&
          (resizeSnappedSize.width !== widget.width || resizeSnappedSize.height !== widget.height)) {
        const newW = resizeSnappedSize.width;
        const newH = resizeSnappedSize.height;
        dashboard.widgets = dashboard.widgets?.map((w) =>
          w.id === widget.id ? { ...w, width: newW, height: newH } : w
        );
        resizing = null;
        resizeSnappedSize = null;
        if (selectedWidget?.id === widget.id) {
          selectedWidget = { ...selectedWidget, width: newW, height: newH };
        }
        try {
          await dashboards.updateWidget(dashboard.id, widget.id, { width: newW, height: newH });
          markDraftChanged();
        } catch (e) {
          console.error('Failed to persist widget size', e);
          dashboard.widgets = dashboard.widgets?.map((w) =>
            w.id === widget.id ? { ...w, width: widget.width, height: widget.height } : w
          );
        }
      } else {
        resizing = null;
        resizeSnappedSize = null;
      }
      return;
    }

    // Handle drag release.
    if (!dragging || !dashboard || !gridConfig) {
      dragging = null;
      return;
    }

    const widget = dragging;

    if (!didDrag) {
      dragging = null;
      return;
    }

    // Update position before clearing dragging state to avoid a visual snap-back.
    const movedRegion = dragRegionId !== (widget.section_id ?? '');
    if (dragValid && dragSnappedPos &&
        (movedRegion || dragSnappedPos.x !== widget.x || dragSnappedPos.y !== widget.y)) {
      const newX = dragSnappedPos.x;
      const newY = dragSnappedPos.y;
      const newSection = dragRegionId || null;
      dashboard.widgets = dashboard.widgets?.map((w) =>
        w.id === widget.id ? { ...w, x: newX, y: newY, section_id: newSection } : w
      );
      dragging = null;
      dragSnappedPos = null;
      if (selectedWidget?.id === widget.id) {
        selectedWidget = { ...selectedWidget, x: newX, y: newY, section_id: newSection };
      }
      try {
        await dashboards.updateWidget(dashboard.id, widget.id, {
          section_id: dragRegionId,
          x: newX,
          y: newY,
        });
        markDraftChanged();
      } catch (e) {
        console.error('Failed to persist widget position', e);
        dashboard.widgets = dashboard.widgets?.map((w) =>
          w.id === widget.id ? { ...w, x: widget.x, y: widget.y, section_id: widget.section_id } : w
        );
      }
    } else {
      dragging = null;
      dragSnappedPos = null;
    }
  }

  function onResizePointerDown(e: PointerEvent, widget: DashboardWidget) {
    if (!gridConfig || !containerEl) return;
    resizing = widget;
    resizeSnappedSize = null;
    resizeValid = false;
    didResize = false;
    resizeStartX = e.clientX;
    resizeStartY = e.clientY;
    e.preventDefault();
    e.stopPropagation();
  }

  // drawGridLines draws the cell edges the snap lands on. Columns run the full
  // height of the surface; rows are drawn per region, since each section's
  // rows start from its own top rather than from the page's.
  function drawGridLines(): string {
    if (!gridConfig || containerWidth === 0 || !layout) return '';
    const gap = gridConfig.gap ?? 8;
    const padding = gridConfig.padding ?? 8;
    const cell = cellSize(gridConfig);
    const stroke = 'var(--theme-surface1, #45475a)';
    const height = layout.totalHeight;
    let lines = '';

    // Draw dashed lines at both edges of each cell, making the gap between widgets visible.
    for (let col = 0; col < gridConfig.cols; col++) {
      const left = padding + col * (cell.w + gap);
      const right = left + cell.w;
      lines += `<line x1="${left}" y1="0" x2="${left}" y2="${height}" stroke="${stroke}" stroke-width="0.5" stroke-dasharray="4,4"/>`;
      lines += `<line x1="${right}" y1="0" x2="${right}" y2="${height}" stroke="${stroke}" stroke-width="0.5" stroke-dasharray="4,4"/>`;
    }
    for (const region of layout.regions) {
      if (region.collapsed) continue;
      const rows = scrolling ? region.rows : gridConfig.rows;
      for (let row = 0; row < rows; row++) {
        const top = region.contentTop + row * (cell.h + gap);
        const bottom = top + cell.h;
        lines += `<line x1="0" y1="${top}" x2="${containerWidth}" y2="${top}" stroke="${stroke}" stroke-width="0.5" stroke-dasharray="4,4"/>`;
        lines += `<line x1="0" y1="${bottom}" x2="${containerWidth}" y2="${bottom}" stroke="${stroke}" stroke-width="0.5" stroke-dasharray="4,4"/>`;
      }
    }
    return lines;
  }

  function dropIndicatorStyle(): string {
    if (!dragging || !dragSnappedPos || !gridConfig) return 'display:none;';
    const region = regions.find((r) => r.id === dragRegionId) ?? regions[0];
    if (!region) return 'display:none;';
    const pos = widgetRect(
      { x: dragSnappedPos.x, y: dragSnappedPos.y, width: dragging.width, height: dragging.height },
      gridConfig,
      region.contentTop
    );
    const color = dragValid
      ? 'var(--theme-success, #a6e3a1)'
      : 'var(--theme-error, #f38ba8)';
    const bg = dragValid
      ? 'rgba(166, 227, 161, 0.1)'
      : 'rgba(243, 139, 168, 0.1)';
    return `position:absolute;left:${pos.left}px;top:${pos.top}px;width:${pos.width}px;height:${pos.height}px;border:2px dashed ${color};border-radius:8px;pointer-events:none;z-index:90;background:${bg};`;
  }

  function dragWidgetStyle(widget: DashboardWidget): string {
    if (!gridConfig) return '';
    if (dragging?.id === widget.id && didDrag) {
      const size = widgetRect(
        { x: 0, y: 0, width: widget.width, height: widget.height },
        gridConfig,
        0
      );
      return `position:absolute;left:${dragCurrentLeft}px;top:${dragCurrentTop}px;width:${size.width}px;height:${size.height}px;opacity:0.4;z-index:100;pointer-events:none;box-shadow:none;border-color:transparent;`;
    }
    if (resizing?.id === widget.id && didResize && resizeSnappedSize) {
      const region = regionOf(widget);
      const pos = widgetRect(
        { x: widget.x, y: widget.y, width: resizeSnappedSize.width, height: resizeSnappedSize.height },
        gridConfig,
        region?.contentTop ?? 0
      );
      const outline = resizeValid
        ? '2px solid var(--theme-success, #a6e3a1)'
        : '2px solid var(--theme-error, #f38ba8)';
      return `position:absolute;left:${pos.left}px;top:${pos.top}px;width:${pos.width}px;height:${pos.height}px;outline:${outline};outline-offset:-2px;`;
    }
    return widgetStyle(widget);
  }
</script>

<svelte:window
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onresize={onWindowResize}
  onpointerdown={(e) => { if (previewMenuOpen && !(e.target as Element).closest?.('.preview-variables')) previewMenuOpen = false; }}
/>

{#snippet regionEditCell(widget: DashboardWidget, _region: RegionId)}
  {@const cue = canvasCue(widget)}
  <div
    class="widget-cell card region-edit-cell"
    class:selected={selectedWidget?.id === widget.id}
    class:transparent-tile={isTransparent(widget.display?.style)}
    style="width:100%;height:100%;padding:{dashboard?.widget_padding ?? 4}px;{tileStyleCSS(widget.display?.style)}"
    onclick={() => selectWidget(widget)}
    onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); selectWidget(widget); } }}
    role="button"
    tabindex="0"
    title={widget.display?.name || widget.widget?.name || ''}
  >
    <WidgetRenderer
      packageName={widget.widget?.package_name || ''}
      tagName={widget.widget?.tag_name || ''}
      entryPoint={widget.widget?.entry_point || 'index.js'}
      config={canvasConfig(widget)}
    />
    {#if cue}<TileCue {cue} />{/if}
    <button
      class="remove-btn"
      onclick={(e: MouseEvent) => { e.stopPropagation(); removeWidget(widget.id); }}
      title="Remove widget"
    >
      <i class="las la-times"></i>
    </button>
  </div>
{/snippet}

<div class="flex flex-col h-[calc(100vh-60px)]">
  {#if loading}
    <div class="flex items-center justify-center flex-1 text-text-muted">
      Loading editor...
    </div>
  {:else if !dashboard}
    <div class="flex items-center justify-center flex-1 text-text-muted">
      Dashboard not found.
    </div>
  {:else}
    <!-- Toolbar: same chrome as the flow editor, since both are the same job. -->
    <div class="flex items-center gap-3 px-4 py-2 border-b"
         style="border-color: var(--theme-border, var(--theme-surface0)); background-color: var(--theme-mantle);">
      <div class="flex items-center gap-2 min-w-0">
        <h1 class="font-bold truncate">{dashboard.name}</h1>
        <span class="text-xs opacity-60">
          {#if regionMode}
            regions · {dashboard.grid_cols} cols · {dashboard.row_height || DEFAULT_ROW_HEIGHT}px rows
          {:else if scrolling}
            {dashboard.grid_cols} cols · {dashboard.row_height || DEFAULT_ROW_HEIGHT}px rows
          {:else}
            {dashboard.grid_cols}x{dashboard.grid_rows}
          {/if}
        </span>
      </div>

      <div class="flex items-center gap-2 text-xs">
        {#if dashboard.deployed_version > 0}
          <span class="badge badge-deployed">
            <i class="las la-check-circle mr-1"></i>v{dashboard.deployed_version}
          </span>
          {#if draftChanged}
            <span class="badge badge-draft" title="The draft differs from the version viewers see">Draft changes</span>
          {/if}
        {:else}
          <span class="badge badge-undeployed" title="Viewers see the draft until it is deployed">Not deployed</span>
        {/if}
      </div>

      <div class="flex-1"></div>

      {#if previewPickers.length > 0}
        <div class="preview-variables">
          <button
            class="btn btn-secondary btn-sm"
            title="Preview the canvas with other variable values; viewers are not changed"
            aria-expanded={previewMenuOpen}
            onclick={() => (previewMenuOpen = !previewMenuOpen)}
          >
            <i class="las la-dollar-sign mr-1"></i>Preview
          </button>
          {#if previewMenuOpen}
            <div class="preview-variables-panel">
              <p class="hint">Previews the canvas only. Viewers keep their values.</p>
              {#each previewPickers as variable (variable.name)}
                <label class="preview-variable">
                  <span>{variable.label || variable.name}</span>
                  <select
                    class="input text-xs"
                    value={previewValues[variable.name]}
                    onchange={(e) => (previewVariableValues = { ...previewVariableValues, [variable.name]: (e.currentTarget as HTMLSelectElement).value })}
                  >
                    {#each optionsOf(variable) as option, i (i)}
                      <option value={option.value}>{optionText(option)}</option>
                    {/each}
                  </select>
                </label>
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      <EditorAssistant kind="dashboard" id={dashboard.id} name={dashboard.name} onchange={refreshAssistantDraft} />
      <button class="btn btn-secondary btn-sm" onclick={() => (showWidgetPalette = !showWidgetPalette)}>
        <i class="las la-plus mr-1"></i>Widget
      </button>
      {#if scrolling}
        <button class="btn btn-secondary btn-sm" onclick={addSection} title="Add a collapsible section at the bottom">
          <i class="las la-plus mr-1"></i>Section
        </button>
      {/if}
      <button class="btn btn-secondary btn-sm" onclick={addPage} title="Add a page; widgets without a page show on every page">
        <i class="las la-plus mr-1"></i>Page
      </button>
      <button class="btn btn-secondary btn-sm" onclick={openThemeSelector} title="Theme" aria-label="Theme">
        <i class="las la-palette"></i>
      </button>
      <button class="btn btn-secondary btn-sm" onclick={openSettings} title="Settings" aria-label="Dashboard settings">
        <i class="las la-cog"></i>
      </button>
      <button
        class="btn btn-secondary btn-sm"
        onclick={exportDashboard}
        title="Export the draft as a portable JSON document"
        aria-label="Export dashboard"
      >
        <i class="las la-file-export"></i>
      </button>
      <button
        class="btn btn-secondary btn-sm"
        onclick={() => navigate(`/dashboard/${id}/render`)}
        title="Render this dashboard to an image, or on a schedule"
        aria-label="Render dashboard"
      >
        <i class="las la-camera"></i>
      </button>
      <button class="btn btn-secondary btn-sm" onclick={openHistory} title="Version history" aria-label="Version history">
        <i class="las la-history"></i>
      </button>
      <button class="btn btn-primary btn-sm" disabled={deploying} onclick={deploy}>
        <i class="las la-rocket mr-1"></i>{deploying ? 'Deploying...' : 'Deploy'}
      </button>
      <button class="btn btn-secondary btn-sm" onclick={() => navigate(`/dashboard/${id}`)}>
        <i class="las la-eye mr-1"></i>View
      </button>
    </div>

    {#if pages.length > 0}
      <!-- Page bar: picks which page the canvas shows and new widgets land on. -->
      <div class="page-bar" role="tablist" aria-label="Pages">
        <button class="page-tab" class:active={pageFilter === ''} role="tab" aria-selected={pageFilter === ''} onclick={() => (pageFilter = '')}>
          All pages
        </button>
        {#each pages as page, i (page.id)}
          <div class="page-tab" class:active={pageFilter === page.id}>
            {#if renamingPage === page.id}
              <!-- svelte-ignore a11y_autofocus -->
              <input
                class="page-title-input"
                value={page.title}
                aria-label="Page title"
                autofocus
                onblur={(e: FocusEvent) => renamePage(page, (e.currentTarget as HTMLInputElement).value)}
                onkeydown={(e: KeyboardEvent) => {
                  if (e.key === 'Enter') (e.currentTarget as HTMLInputElement).blur();
                  if (e.key === 'Escape') renamingPage = '';
                }}
              />
            {:else}
              <button
                class="page-name"
                role="tab"
                aria-selected={pageFilter === page.id}
                title="Double-click to rename"
                onclick={() => (pageFilter = page.id)}
                ondblclick={() => (renamingPage = page.id)}
              >{i + 1}. {page.title}</button>
            {/if}
            <span class="page-actions">
              <button title="Move page left" aria-label="Move page left" onclick={() => movePage(page, -1)}><i class="las la-angle-left"></i></button>
              <button title="Move page right" aria-label="Move page right" onclick={() => movePage(page, 1)}><i class="las la-angle-right"></i></button>
              <button class="danger" title="Remove page" aria-label="Remove page" onclick={() => removePage(page)}><i class="las la-times"></i></button>
            </span>
          </div>
        {/each}
        <span class="page-hint">
          {#if pageFilter}
            Showing this page and widgets on every page. New widgets land on this page.
          {:else}
            Showing every widget. New widgets appear on every page.
          {/if}
        </span>
      </div>
    {/if}

    <div class="flex flex-1 overflow-hidden">
      <!-- Grid Canvas -->
      <div
        bind:this={containerEl}
        bind:clientWidth={containerWidth}
        bind:clientHeight={containerHeight}
        class="flex-1 relative"
        class:overflow-y-auto={scrolling || regionMode}
        class:overflow-hidden={!scrolling && !regionMode}
        style="background-color: var(--theme-base);"
        role="application"
        aria-label="Dashboard grid editor"
      >
        <!-- The scrolling surface everything is positioned against. In the fit
             layout it is exactly the container; in the scrolling one it is as
             tall as the sections need and the container scrolls over it. -->
        <div class="relative" style={regionMode ? 'min-height:100%;' : scrolling && layout ? `height:${layout.totalHeight}px;` : 'height:100%;'}>
          {#if regionMode}
            <RegionLayout
              widgets={canvasWidgets}
              gridCols={dashboard.grid_cols}
              rowHeight={dashboard.row_height || DEFAULT_ROW_HEIGHT}
              gap={dashboard.gap ?? 8}
              padding={dashboard.padding ?? 8}
              editing
              selectedRegion={targetRegion}
              cell={regionEditCell}
              onregionclick={(region) => (targetRegion = region)}
            />
          {/if}

          <!-- Grid lines -->
          {#if !regionMode && containerWidth > 0 && gridConfig && layout}
            <svg class="absolute inset-0 pointer-events-none" width={containerWidth} height={layout.totalHeight}>
              {@html containerWidth && containerHeight && drawGridLines()}
            </svg>
          {/if}

          <!-- Drop indicator -->
          {#if dragging && didDrag}
            <div style={dropIndicatorStyle()}></div>
          {/if}

          {#if !regionMode && gridConfig && containerWidth > 0}
            {#each regions as region (region.id)}
              {#if region.section}
                <div class="section-bar" style="top:{region.headerTop}px;left:{dashboard.padding ?? 8}px;right:{dashboard.padding ?? 8}px;height:{SECTION_HEADER_HEIGHT}px;">
                  <button
                    class="section-toggle"
                    title={region.collapsed ? 'Expand section' : 'Collapse section'}
                    aria-expanded={!region.collapsed}
                    onclick={() => patchSection(region.section!, { collapsed: !region.collapsed })}
                  >
                    <i class="las {region.collapsed ? 'la-angle-right' : 'la-angle-down'}"></i>
                  </button>
                  <input
                    class="section-title-input"
                    value={region.section.title}
                    aria-label="Section title"
                    onchange={(e: Event) => patchSection(region.section!, { title: (e.currentTarget as HTMLInputElement).value })}
                  />
                  {#if region.collapsed}
                    {@const hidden = (dashboard.widgets ?? []).filter((w) => w.section_id === region.section!.id).length}
                    <span class="section-count">{hidden} widget{hidden === 1 ? '' : 's'}</span>
                  {/if}
                  <div class="section-actions">
                    <button title="Move section up" aria-label="Move section up" onclick={() => moveSection(region.section!, -1)}>
                      <i class="las la-arrow-up"></i>
                    </button>
                    <button title="Move section down" aria-label="Move section down" onclick={() => moveSection(region.section!, 1)}>
                      <i class="las la-arrow-down"></i>
                    </button>
                    <button class="danger" title="Remove section" aria-label="Remove section" onclick={() => removeSection(region.section!)}>
                      <i class="las la-trash"></i>
                    </button>
                  </div>
                </div>
              {/if}

              {#each region.widgets as widget (widget.id)}
                {@const cue = canvasCue(widget)}
                <div
                  class="widget-cell card"
                  class:selected={selectedWidget?.id === widget.id}
                  class:dragging-widget={dragging?.id === widget.id && didDrag}
                  class:transparent-tile={isTransparent(widget.display?.style)}
                  style="{containerWidth && containerHeight && dragWidgetStyle(widget)}padding:{dashboard.widget_padding ?? 4}px;{tileStyleCSS(widget.display?.style)}"
                  onpointerdown={(e: PointerEvent) => onWidgetPointerDown(e, widget)}
                  onclick={() => { if (!didDrag && !didResize) selectWidget(widget); }}
                  onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); selectWidget(widget); } }}
                  role="button"
                  tabindex="0"
                >
                  <WidgetRenderer
                    packageName={widget.widget?.package_name || ''}
                    tagName={widget.widget?.tag_name || ''}
                    entryPoint={widget.widget?.entry_point || 'index.js'}
                    config={canvasConfig(widget)}
                  />
                  {#if cue}<TileCue {cue} />{/if}
                  <button
                    class="remove-btn"
                    onclick={(e: MouseEvent) => { e.stopPropagation(); removeWidget(widget.id); }}
                    title="Remove widget"
                  >
                    <i class="las la-times"></i>
                  </button>
                  <button
                    class="resize-handle"
                    aria-label="Resize widget"
                    onpointerdown={(e: PointerEvent) => { e.stopPropagation(); onResizePointerDown(e, widget); }}
                    onclick={(e: MouseEvent) => e.stopPropagation()}
                  ></button>
                </div>
              {/each}
            {/each}
          {/if}
        </div>
      </div>

    </div>

    <!-- Widget Config Overlay -->
    {#if selectedWidget}
      <div
        class="fixed inset-0 z-40"
        style="background: rgba(0,0,0,0.3);"
        onclick={() => (selectedWidget = null)}
        onkeydown={(e: KeyboardEvent) => { if (e.key === 'Escape') selectedWidget = null; }}
        role="dialog"
        aria-modal="true"
        aria-label="Widget configuration"
        tabindex="-1"
        transition:fade={{ duration: 150 }}
      ></div>
      <div
        class="fixed top-0 right-0 bottom-0 w-80 z-50 overflow-y-auto p-4"
        style="background-color: var(--theme-mantle); border-left: 1px solid var(--theme-border, var(--theme-surface0)); box-shadow: -4px 0 12px rgba(0,0,0,0.2);"
        role="region"
        aria-label="Widget settings panel"
        transition:fly={{ x: 320, duration: 200 }}
      >
        <h3 class="font-semibold mb-3">{selectedWidget.display?.name || selectedWidget.widget?.name || 'Widget'}</h3>
        <PlacementDisplayPanel
          widget={selectedWidget}
          {pages}
          layoutMode={dashboard.layout_mode}
          variables={variableDefs}
          onchange={(patch) => {
            if (!selectedWidget) return;
            if (patch.region !== undefined) void changeRegion(selectedWidget, patch.region);
            else void patchPlacement(selectedWidget, patch);
          }}
          onmove={(delta) => { if (selectedWidget) void moveInRegion(selectedWidget, delta); }}
        />
        {#if selectedWidget.widget?.config_tag_name}
          <!-- A panel is created for one placement: switching to another
               widget, even of another package, must mount its own. -->
          {#key `${selectedWidget.id}:${panelEpoch}`}
          <WidgetConfigRenderer
            packageName={selectedWidget.widget.package_name || ''}
            configTagName={selectedWidget.widget.config_tag_name}
            entryPoint={selectedWidget.widget.entry_point || 'index.js'}
            config={(() => {
              try {
                const parsed = JSON.parse(selectedWidget.config || '{}');
                parsed.dashboardWidgetId = selectedWidget.id;
                parsed.dashboardId = dashboard?.id || '';
                return JSON.stringify(parsed);
              } catch {
                return JSON.stringify({ dashboardWidgetId: selectedWidget.id, dashboardId: dashboard?.id || '' });
              }
            })()}
            onconfigchange={handleCustomConfigChange}
          />
          {/key}
        {:else}
          <p class="text-sm text-text-muted">No configurable properties.</p>
        {/if}
      </div>
    {/if}

    <!-- Widget Palette Modal -->
    {#if showWidgetPalette}
      <Modal title="Add Widget" onclose={() => (showWidgetPalette = false)} maxWidth="lg" maxHeight="80vh">
        {#if availableWidgets.length === 0}
          <p class="text-text-muted">No widgets available.</p>
        {:else}
          <div class="flex flex-col gap-2">
            {#each availableWidgets as widget}
              <button
                class="card p-3 text-left hover:opacity-80 transition-opacity"
                onclick={() => addWidget(widget)}
              >
                <div class="font-semibold">{widget.name}</div>
                <div class="text-sm text-text-muted">{widget.description}</div>
                <div class="text-xs text-text-muted mt-1">v{widget.version}</div>
              </button>
            {/each}
          </div>
        {/if}
      </Modal>
    {/if}

    <!-- Theme Selector Modal -->
    {#if showThemeSelector}
      <Modal title="Select Theme" onclose={cancelThemeSelector} maxWidth="2xl" maxHeight="80vh">
        {#if themeList.length === 0}
          <p class="text-text-muted mb-4">No themes available.</p>
        {:else}
          <div class="grid grid-cols-2 gap-2 overflow-y-auto flex-1 mb-4">
            {#each themeList as theme}
              <button
                class="theme-preview-card"
                class:selected-theme={selectedThemeId === theme.id}
                style={themeCardStyle(theme)}
                onclick={() => selectThemePreview(theme.id)}
              >
                <div class="flex items-start justify-between gap-2">
                  <div class="flex-1 min-w-0">
                    <div class="font-semibold">{theme.name}</div>
                    <div class="text-sm mt-0.5" style="color: var(--card-muted);">{theme.description}</div>
                    <div class="flex gap-1 mt-1.5">
                      {#each swatchColors(theme) as color}
                        <span class="theme-swatch" style:background-color={color}></span>
                      {/each}
                    </div>
                  </div>
                  <div class="flex-shrink-0 text-right">
                    {#if theme.author}
                      <div class="text-xs" style="color: var(--card-muted);">{theme.author}</div>
                    {/if}
                    <div class="text-xs" style="color: var(--card-muted); opacity: 0.6;">v{theme.version}</div>
                  </div>
                </div>
              </button>
            {/each}
          </div>
        {/if}
        <div class="flex gap-2 justify-end">
          {#if selectedThemeId !== null}
            <button class="btn btn-secondary text-sm" onclick={() => selectThemePreview(null)}>
              <i class="las la-undo mr-1"></i>Clear & Use Default
            </button>
          {/if}
          <button class="btn btn-primary text-sm" onclick={saveTheme} disabled={savingTheme}>
            <i class="las la-save mr-1"></i>{savingTheme ? 'Saving...' : 'Save'}
          </button>
        </div>
      </Modal>
    {/if}

    <!-- Version History Modal -->
    {#if showHistory}
      <Modal title="Version History" onclose={() => (showHistory = false)}>
        {#if historyLoading}
          <p class="text-sm opacity-60">Loading versions...</p>
        {:else if historyVersions.length === 0}
          <p class="text-sm opacity-60">No deployed versions yet. Deploy the dashboard to create version 1.</p>
        {:else}
          <div class="flex flex-col gap-1 max-h-96 overflow-y-auto">
            {#each historyVersions as v (v.version)}
              <div class="version-row">
                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="font-bold text-sm">v{v.version}</span>
                    {#if v.version === dashboard.deployed_version}
                      <span class="badge-current">deployed</span>
                    {/if}
                  </div>
                  <div class="text-xs opacity-60 truncate">
                    {v.widget_count} widget{v.widget_count === 1 ? '' : 's'}
                    · {relTime(v.created_at)}
                    {#if v.created_by?.username}
                      · {v.created_by.username}
                    {/if}
                  </div>
                </div>
                <button
                  class="btn btn-secondary btn-sm shrink-0"
                  disabled={restoring}
                  onclick={() => restoreVersion(v.version)}
                  title="Copy this version's layout into the draft"
                >
                  <i class="las la-undo-alt mr-1"></i>Restore
                </button>
              </div>
            {/each}
          </div>
          <p class="text-xs opacity-50 mt-3">
            Restoring copies the version's layout into the editable draft. Viewers keep seeing the deployed version until you deploy again.
          </p>
        {/if}
      </Modal>
    {/if}

    <!-- Settings Modal -->
    {#if showSettings}
      <Modal title="Dashboard settings" maxWidth="4xl" onclose={() => (showSettings = false)}>
        <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveSettings(); }} class="flex flex-col gap-4">
          <div class="settings-tabs" role="tablist" aria-label="Settings sections">
            {#each SETTINGS_TABS as tab (tab.id)}
              <button
                type="button"
                role="tab"
                class="settings-tab"
                class:active={settingsTab === tab.id}
                aria-selected={settingsTab === tab.id}
                onclick={() => (settingsTab = tab.id)}
              >
                <i class="las {tab.icon}"></i>{tab.label}
                {#if tab.id === 'variables' && settingsVariables.length > 0}
                  <span class="tab-count">{settingsVariables.length}</span>
                {/if}
              </button>
            {/each}
          </div>

          <div class="settings-panel" role="tabpanel">
            {#if settingsTab === 'general'}
              <FormField label="Name" id="set-name">
                <input id="set-name" class="input w-full" bind:value={settingsName} />
              </FormField>
              <FormField label="Slug" id="set-slug" error={settingsSlugError}>
                <input
                  id="set-slug"
                  class="input w-full"
                  bind:value={settingsSlug}
                  oninput={() => { settingsSlugAutoGen = false; settingsSlugError = ''; }}
                  onblur={() => { if (!settingsSlug.trim()) { settingsSlugAutoGen = true; settingsSlug = slugify(settingsName); } }}
                />
              </FormField>
              <FormField label="Description" id="set-desc">
                <textarea id="set-desc" class="input w-full" rows="3" bind:value={settingsDescription}></textarea>
              </FormField>
              {#if ownedGroups.length > 0}
                <FormField label="Folder" id="set-group">
                  <select id="set-group" class="input w-full" bind:value={settingsGroupId}>
                    <option value={null}>No folder</option>
                    {#each ownedGroups as g}
                      <option value={g.id}>{g.name}</option>
                    {/each}
                  </select>
                </FormField>
              {/if}

            {:else if settingsTab === 'layout'}
              <FormField label="Layout" id="set-layout">
                <select id="set-layout" class="input w-full" bind:value={settingsLayoutMode}>
                  <option value="fit">Fit to screen</option>
                  <option value="scroll">Scrolling</option>
                  <option value="regions">Regions</option>
                </select>
              </FormField>
              <p class="hint">
                {#if settingsLayoutMode === 'scroll'}
                  Columns scale to the width and every row is the height below, so the dashboard keeps
                  going past the fold. Sections are added from the toolbar.
                {:else if settingsLayoutMode === 'regions'}
                  Widgets anchor to regions such as the top left corner or the lower third and stack in
                  order, the way a mirror is laid out. A widget is its height in rows tall and its width
                  in columns of its region wide. Grid positions are kept, so switching back restores them.
                {:else}
                  Rows and columns both scale to the viewport, so nothing scrolls and nothing is off screen.
                {/if}
              </p>
              <div class="grid grid-cols-2 gap-4">
                {#if settingsLayoutMode !== 'fit'}
                  <FormField label="Row height (px)" id="set-rowheight">
                    <input id="set-rowheight" type="number" class="input w-full" bind:value={settingsRowHeight} min="16" max="400" />
                  </FormField>
                {:else}
                  <FormField label="Grid rows" id="set-rows">
                    <input id="set-rows" type="number" class="input w-full" bind:value={settingsGridRows} min="1" max="48" />
                  </FormField>
                {/if}
                <FormField label="Grid columns" id="set-cols">
                  <input id="set-cols" type="number" class="input w-full" bind:value={settingsGridCols} min="1" max="48" />
                </FormField>
              </div>
              <div class="grid grid-cols-3 gap-4">
                <FormField label="Viewport padding" id="set-padding">
                  <input id="set-padding" type="number" class="input w-full" bind:value={settingsPadding} min="0" max="64" />
                </FormField>
                <FormField label="Widget gap" id="set-gap">
                  <input id="set-gap" type="number" class="input w-full" bind:value={settingsGap} min="0" max="64" />
                </FormField>
                <FormField label="Widget padding" id="set-wpad">
                  <input id="set-wpad" type="number" class="input w-full" bind:value={settingsWidgetPadding} min="0" max="64" />
                </FormField>
              </div>

            {:else if settingsTab === 'screen'}
              <p class="hint">
                For a panel or mirror that stays on. Times use the viewer's clock. A touch, key, or wheel
                wakes a dimmed screen for the idle time, or 30 seconds without one. A Dashboard Command's
                dim level replaces these until it is undimmed. Renders ignore all of them.
              </p>
              {#if pages.length > 1}
                <FormField label="Rotate pages every (seconds)" id="set-rotate">
                  <input id="set-rotate" type="number" class="input w-full" bind:value={settingsPageInterval} min="0" max="3600" />
                </FormField>
                <p class="hint" style="margin-top: -0.5rem;">0 keeps the page until a viewer or a Dashboard Command changes it. Otherwise 5 to 3600.</p>
              {/if}
              <div class="screen-option">
                <label class="flex items-center gap-2">
                  <input type="checkbox" bind:checked={settingsNightEnabled} />
                  <span class="text-sm">Dim at night</span>
                </label>
                {#if settingsNightEnabled}
                  <div class="grid grid-cols-3 gap-2">
                    <FormField label="From" id="set-night-from"><input id="set-night-from" type="time" class="input w-full" bind:value={settingsNightFrom} /></FormField>
                    <FormField label="To" id="set-night-to"><input id="set-night-to" type="time" class="input w-full" bind:value={settingsNightTo} /></FormField>
                    <FormField label="Level (%)" id="set-night-level"><input id="set-night-level" type="number" class="input w-full" bind:value={settingsNightLevel} min="0" max="100" /></FormField>
                  </div>
                {/if}
              </div>
              <div class="screen-option">
                <label class="flex items-center gap-2">
                  <input type="checkbox" bind:checked={settingsIdleEnabled} />
                  <span class="text-sm">Dim when nobody touches it</span>
                </label>
                {#if settingsIdleEnabled}
                  <div class="grid grid-cols-2 gap-2">
                    <FormField label="After (seconds)" id="set-idle-after"><input id="set-idle-after" type="number" class="input w-full" bind:value={settingsIdleAfter} min="1" max="86400" /></FormField>
                    <FormField label="Level (%)" id="set-idle-level"><input id="set-idle-level" type="number" class="input w-full" bind:value={settingsIdleLevel} min="0" max="100" /></FormField>
                  </div>
                {/if}
              </div>
              <FormField label="Burn-in shift (pixels)" id="set-shift">
                <input id="set-shift" type="number" class="input w-full" bind:value={settingsPixelShift} min="0" max="16" />
              </FormField>

            {:else if settingsTab === 'variables'}
              <VariablesEditor bind:drafts={settingsVariables} widgets={dashboard.widgets ?? []} dashboardId={dashboard.id} />

            {:else if settingsTab === 'sharing'}
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={settingsIsPublic} />
                <span class="text-sm">Public dashboard</span>
              </label>
              <p class="hint" style="margin-top: -0.5rem;">Anyone with the link may view it, and read the entities, controls and devices its widgets are bound to. Nothing else on those connections is exposed.</p>
              {#if settingsIsPublic}
                <label class="flex items-center gap-2">
                  <input type="checkbox" bind:checked={settingsPublicControl} />
                  <span class="text-sm">Allow anonymous visitors to operate it</span>
                </label>
                <p class="hint" style="margin-top: -0.5rem;">Off by default: publishing a dashboard shows the house, it does not hand out the switches. Signed-in people you shared it with can always operate it.</p>
              {/if}
              <div class="share-link">
                <div>
                  <div class="text-sm">People and groups</div>
                  <p class="hint">Share with named users or groups at view, edit, or admin access.</p>
                </div>
                <button type="button" class="btn btn-secondary btn-sm" onclick={() => navigate(`/dashboard/${id}/share`)}>
                  <i class="las la-user-friends mr-1"></i>Manage access
                </button>
              </div>
            {/if}
          </div>

          <div class="settings-footer">
            {#if settingsError}
              <p class="settings-error" role="alert">{settingsError}</p>
            {/if}
            <button type="submit" class="btn btn-primary" disabled={saving}>
              <i class="las la-save mr-1"></i>{saving ? 'Saving...' : 'Save'}
            </button>
          </div>
        </form>
      </Modal>
    {/if}
  {/if}
</div>

{#if toast}
  <div class="toast" role="alert">
    <i class="las la-info-circle mr-1"></i>{toast}
  </div>
{/if}

<style>
  .badge {
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    border: 1px solid;
    white-space: nowrap;
  }

  .badge-deployed {
    color: var(--theme-success, #a6e3a1);
    border-color: var(--theme-success, #a6e3a1);
  }

  .badge-draft {
    color: var(--theme-warning, #f9e2af);
    border-color: var(--theme-warning, #f9e2af);
  }

  .badge-undeployed {
    color: var(--theme-overlay0, #6c7086);
    border-color: var(--theme-overlay0, #6c7086);
  }

  .version-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.45rem 0.6rem;
    border-radius: var(--theme-radius-sm, 4px);
    border: 1px solid var(--theme-surface0, #313244);
  }

  .badge-current {
    padding: 0.05rem 0.4rem;
    border-radius: 999px;
    border: 1px solid var(--theme-success, #a6e3a1);
    color: var(--theme-success, #a6e3a1);
    font-size: 0.65rem;
  }

  .toast {
    position: fixed;
    bottom: 1.25rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 60;
    max-width: 28rem;
    padding: 0.6rem 1rem;
    border-radius: var(--theme-radius-md, 8px);
    background-color: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-accent, #89b4fa);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  }

  .widget-cell {
    display: flex;
    overflow: hidden;
    transition: box-shadow 0.2s ease, outline 0.15s ease;
    cursor: grab;
    touch-action: none;
  }

  .region-edit-cell {
    position: relative;
    cursor: pointer;
  }

  .page-bar {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.3rem 1rem;
    overflow-x: auto;
    border-bottom: 1px solid var(--theme-border, var(--theme-surface0));
    background-color: var(--theme-mantle);
    font-size: 0.8rem;
  }

  .page-tab {
    display: flex;
    align-items: center;
    gap: 0.15rem;
    padding: 0.15rem 0.5rem;
    border-radius: var(--theme-radius-sm, 4px);
    border: 1px solid var(--theme-surface1, #45475a);
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    white-space: nowrap;
    cursor: pointer;
  }

  .page-tab.active {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  .page-name {
    background: transparent;
    border: none;
    color: inherit;
    cursor: pointer;
  }

  .page-title-input {
    width: 8rem;
    background: var(--theme-base);
    border: 1px solid var(--theme-accent, #89b4fa);
    border-radius: var(--theme-radius-sm, 4px);
    color: var(--theme-text, #cdd6f4);
    padding: 0 0.3rem;
  }

  .page-actions {
    display: none;
  }

  .page-tab:hover .page-actions,
  .page-actions:focus-within {
    display: inline-flex;
  }

  .page-actions button {
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0 0.1rem;
  }

  .page-actions button:hover {
    color: var(--theme-accent, #89b4fa);
  }

  .page-actions button.danger:hover {
    color: var(--theme-error, #f38ba8);
  }

  .page-hint {
    margin-left: 0.5rem;
    opacity: 0.55;
    font-size: 0.7rem;
    white-space: nowrap;
  }

  .preview-variables {
    position: relative;
  }

  .preview-variables-panel {
    position: absolute;
    top: calc(100% + 0.4rem);
    right: 0;
    z-index: 60;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 16rem;
    padding: 0.75rem;
    border-radius: 8px;
    background: var(--theme-mantle, #181825);
    border: 1px solid var(--theme-surface1, #45475a);
    box-shadow: var(--theme-shadow-md, 0 4px 6px rgba(0, 0, 0, 0.3));
  }

  .preview-variables-panel .preview-variable {
    justify-content: space-between;
  }

  .preview-variable {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.75rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .settings-tabs {
    display: flex;
    gap: 0.25rem;
    margin-top: -0.5rem;
    border-bottom: 1px solid var(--theme-surface0, #313244);
  }

  .settings-tab {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    font-weight: 600;
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.6;
    cursor: pointer;
  }

  .settings-tab:hover {
    opacity: 0.9;
  }

  .settings-tab.active {
    opacity: 1;
    border-bottom-color: var(--theme-accent, #89b4fa);
  }

  .tab-count {
    min-width: 1.1rem;
    padding: 0 0.3rem;
    font-size: 0.7rem;
    line-height: 1.1rem;
    text-align: center;
    border-radius: 999px;
    background: var(--theme-surface1, #45475a);
  }

  /* A fixed floor keeps the dialog from jumping as tabs of different
     lengths are switched. */
  .settings-panel {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-height: 22rem;
  }

  .hint {
    font-size: 0.75rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .screen-option {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .share-link {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding-top: 1rem;
    border-top: 1px solid var(--theme-surface0, #313244);
  }

  .settings-footer {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 1rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--theme-surface0, #313244);
  }

  .settings-error {
    flex: 1;
    font-size: 0.8rem;
    color: var(--theme-error, #f38ba8);
  }

  /* A section header is chrome, not a card: it reads as the band's label and
     keeps its controls out of the way until the pointer is on it. */
  .section-bar {
    position: absolute;
    z-index: 20;
    display: flex;
    align-items: center;
    gap: 0.4rem;
    border-bottom: 1px solid var(--theme-surface0, #313244);
  }

  .section-toggle {
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0 0.15rem;
  }

  .section-title-input {
    flex: 1;
    min-width: 0;
    background: transparent;
    border: 1px solid transparent;
    border-radius: var(--theme-radius-sm, 4px);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.95rem;
    font-weight: 600;
    padding: 0.1rem 0.35rem;
  }

  .section-title-input:hover {
    border-color: var(--theme-surface1, #45475a);
  }

  .section-title-input:focus {
    outline: none;
    border-color: var(--theme-accent, #89b4fa);
    background: var(--theme-mantle);
  }

  .section-count {
    font-size: 0.7rem;
    opacity: 0.6;
    white-space: nowrap;
  }

  .section-actions {
    display: flex;
    gap: 0.15rem;
    opacity: 0;
    transition: opacity 0.15s ease;
  }

  .section-bar:hover .section-actions,
  .section-actions:focus-within {
    opacity: 1;
  }

  .section-actions button {
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0 0.25rem;
  }

  .section-actions button:hover {
    color: var(--theme-accent, #89b4fa);
  }

  .section-actions button.danger:hover {
    color: var(--theme-error, #f38ba8);
  }

  /* While editing, a widget is something being arranged rather than a live
     control, so its own contents take no pointer events. Without this an
     interactive widget swallows the click meant to select it: an entity toggle
     or flow button whose root is a disabled <button> can never be configured
     at all, and an enabled one acts on the world instead of opening its
     settings. The remove and resize handles are siblings, so they still work. */
  .widget-cell :global(.widget-container) {
    pointer-events: none;
  }

  .widget-cell.selected {
    outline: 2px solid var(--theme-accent, #89b4fa);
    outline-offset: 1px;
  }

  .widget-cell.dragging-widget {
    cursor: grabbing;
  }

  .remove-btn {
    position: absolute;
    top: 4px;
    right: 4px;
    z-index: 10;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
    border: none;
    font-size: 14px;
    line-height: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    opacity: 0;
    transition: opacity 0.15s ease;
    cursor: pointer;
  }

  .widget-cell:hover .remove-btn {
    opacity: 1;
  }

  .resize-handle {
    position: absolute;
    bottom: 0;
    right: 0;
    z-index: 10;
    width: 18px;
    height: 18px;
    cursor: nwse-resize;
    opacity: 0;
    transition: opacity 0.15s ease;
  }

  .resize-handle::after {
    content: '';
    position: absolute;
    bottom: 4px;
    right: 4px;
    width: 8px;
    height: 8px;
    border-right: 2px solid var(--theme-text, #cdd6f4);
    border-bottom: 2px solid var(--theme-text, #cdd6f4);
    opacity: 0.5;
  }

  .widget-cell:hover .resize-handle {
    opacity: 1;
  }

  .theme-preview-card {
    padding: 0.75rem;
    border-radius: 8px;
    border: 2px solid var(--card-surface1, var(--theme-surface1, #45475a));
    text-align: left;
    cursor: pointer;
    transition: border-color 0.15s, box-shadow 0.15s;
  }

  .theme-preview-card:hover {
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  }

  .selected-theme {
    border-color: var(--card-accent, var(--theme-accent, #89b4fa));
    box-shadow: 0 0 0 1px var(--card-accent, var(--theme-accent, #89b4fa));
  }

  .theme-swatch {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    flex-shrink: 0;
  }
</style>
