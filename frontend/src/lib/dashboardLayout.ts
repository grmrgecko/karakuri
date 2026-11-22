import { cellSize, occupiedRows } from './grid';
import type { GridConfig } from './grid';
import type { DashboardSection, DashboardWidget } from './stores/dashboards';

/**
 * A dashboard is laid out as a stack of regions. A fit dashboard has exactly
 * one — the whole viewport. A scrolling one starts with the sectionless region
 * holding every widget that belongs to no section, then a region per section
 * in order, each headed by a collapsible bar. A widget's Y is measured from the
 * top of its own region, which is what lets a section collapse without
 * rewriting a single placement.
 */
export interface LayoutRegion {
  /** Section UUID, or '' for the sectionless region above the first section. */
  id: string;
  section: DashboardSection | null;
  widgets: DashboardWidget[];
  rows: number;
  collapsed: boolean;
  /** Top of the section header bar; equal to contentTop for the sectionless region. */
  headerTop: number;
  headerHeight: number;
  /** Where row 0 of this region sits, in pixels from the top of the layout. */
  contentTop: number;
  contentHeight: number;
  /** First pixel below the region. */
  bottom: number;
}

export interface LayoutResult {
  regions: LayoutRegion[];
  /** Pixel height the layout needs, including the outer padding. */
  totalHeight: number;
}

/** SECTION_HEADER_HEIGHT is the pixel height of a section's collapse bar. */
export const SECTION_HEADER_HEIGHT = 32;

export interface LayoutOptions {
  /**
   * minRows keeps an empty region tall enough to be a drop target. The editor
   * passes it so a new section is something you can drag a widget into; a
   * viewer leaves an empty section as just its header.
   */
  minRows?: number;
  /** collapsedOverride holds the viewer's own folds, keyed by section ID. */
  collapsedOverride?: Record<string, boolean>;
}

/** rowsToPixels converts a region's row count to its pixel height. */
function rowsToPixels(rows: number, config: GridConfig): number {
  if (rows <= 0) return 0;
  const gap = config.gap ?? 8;
  const cell = cellSize(config);
  return rows * cell.h + (rows - 1) * gap;
}

/**
 * computeRegions stacks a dashboard's regions and hands back the pixel bands
 * each one occupies. Both the editor and the viewer derive every position from
 * this, so a widget cannot land in one place while its section header sits in
 * another.
 */
export function computeRegions(
  widgets: DashboardWidget[],
  sections: DashboardSection[],
  config: GridConfig,
  options: LayoutOptions = {}
): LayoutResult {
  const padding = config.padding ?? 8;
  const gap = config.gap ?? 8;
  const minRows = options.minRows ?? 0;
  const overrides = options.collapsedOverride ?? {};

  const rootWidgets = widgets.filter((w) => !w.section_id);

  // A fit dashboard is one region the size of the viewport; sections belong to
  // the scrolling layout and are ignored here rather than half-drawn.
  if (config.mode !== 'scroll') {
    const contentHeight = Math.max(0, config.containerHeight - 2 * padding);
    return {
      regions: [
        {
          id: '',
          section: null,
          widgets,
          rows: config.rows,
          collapsed: false,
          headerTop: padding,
          headerHeight: 0,
          contentTop: padding,
          contentHeight,
          bottom: padding + contentHeight,
        },
      ],
      totalHeight: config.containerHeight,
    };
  }

  const regions: LayoutRegion[] = [];
  let top = padding;

  // The sectionless region leads, above the first section.
  const rootRows = Math.max(occupiedRows(rootWidgets), rootWidgets.length > 0 ? 0 : minRows);
  const rootHeight = rowsToPixels(rootRows, config);
  regions.push({
    id: '',
    section: null,
    widgets: rootWidgets,
    rows: rootRows,
    collapsed: false,
    headerTop: top,
    headerHeight: 0,
    contentTop: top,
    contentHeight: rootHeight,
    bottom: top + rootHeight,
  });
  top += rootHeight;

  const ordered = [...sections].sort((a, b) => a.sort_order - b.sort_order);
  for (const section of ordered) {
    const own = widgets.filter((w) => w.section_id === section.id);
    const collapsed = overrides[section.id] ?? section.collapsed;
    const rows = collapsed ? 0 : Math.max(occupiedRows(own), minRows);
    const contentHeight = rowsToPixels(rows, config);

    const headerTop = top + gap;
    const contentTop = headerTop + SECTION_HEADER_HEIGHT + (contentHeight > 0 ? gap : 0);
    const bottom = contentTop + contentHeight;
    regions.push({
      id: section.id,
      section,
      widgets: collapsed ? [] : own,
      rows,
      collapsed,
      headerTop,
      headerHeight: SECTION_HEADER_HEIGHT,
      contentTop,
      contentHeight,
      bottom,
    });
    top = bottom;
  }

  return { regions, totalHeight: top + padding };
}

/**
 * regionAt answers which region a pointer is over, which is how a drag decides
 * the section a widget lands in. Collapsed regions are skipped — dropping into
 * a band nobody can see would lose the widget — and a pointer above everything
 * lands in the first region.
 */
export function regionAt(regions: LayoutRegion[], y: number): LayoutRegion {
  let match = regions[0];
  for (const region of regions) {
    if (region.collapsed) continue;
    if (y >= region.headerTop) match = region;
  }
  return match;
}

/** widgetsIn returns the placements of one region, for collision checks. */
export function widgetsIn(widgets: DashboardWidget[], regionID: string): DashboardWidget[] {
  return widgets.filter((w) => (w.section_id ?? '') === regionID);
}
