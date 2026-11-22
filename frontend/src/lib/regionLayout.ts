import type { DashboardWidget } from './stores/dashboards';

/**
 * REGIONS are the named screen regions of the region layout, matching
 * models.DashboardRegions on the server. Rows stack top to bottom; the three
 * thirds share whatever height the top and bottom rows leave.
 */
export const REGIONS = [
  { id: 'top_bar', label: 'Top bar' },
  { id: 'top_left', label: 'Top left' },
  { id: 'top_center', label: 'Top center' },
  { id: 'top_right', label: 'Top right' },
  { id: 'upper_third', label: 'Upper third' },
  { id: 'middle_center', label: 'Middle center' },
  { id: 'lower_third', label: 'Lower third' },
  { id: 'bottom_left', label: 'Bottom left' },
  { id: 'bottom_center', label: 'Bottom center' },
  { id: 'bottom_right', label: 'Bottom right' },
  { id: 'bottom_bar', label: 'Bottom bar' },
  { id: 'fullscreen_below', label: 'Full screen, behind' },
  { id: 'fullscreen_above', label: 'Full screen, in front' },
] as const;

export type RegionId = (typeof REGIONS)[number]['id'];

/** DEFAULT_REGION is where a placement with no region is drawn. */
export const DEFAULT_REGION: RegionId = 'middle_center';

/** regionOf returns the region a placement is drawn in. */
export function regionOf(widget: DashboardWidget): RegionId {
  const region = widget.region as RegionId | undefined;
  return region && REGIONS.some((r) => r.id === region) ? region : DEFAULT_REGION;
}

/** regionLabel names a region for the editor. */
export function regionLabel(id: string): string {
  return REGIONS.find((r) => r.id === id)?.label ?? id;
}

/**
 * groupByRegion sorts placements into their regions, stacked by Y and then
 * X, so the order an author sets is the order they are drawn.
 */
export function groupByRegion(widgets: DashboardWidget[]): Record<RegionId, DashboardWidget[]> {
  const groups = Object.fromEntries(REGIONS.map((r) => [r.id, [] as DashboardWidget[]])) as Record<RegionId, DashboardWidget[]>;
  for (const w of widgets) groups[regionOf(w)].push(w);
  for (const list of Object.values(groups)) list.sort((a, b) => a.y - b.y || a.x - b.x);
  return groups;
}

/**
 * alignOf returns how a region aligns placements narrower than it: left
 * regions to the start, right regions to the end, everything else centered.
 */
export function alignOf(region: RegionId): 'flex-start' | 'center' | 'flex-end' {
  if (region.endsWith('_left')) return 'flex-start';
  if (region.endsWith('_right')) return 'flex-end';
  return 'center';
}
