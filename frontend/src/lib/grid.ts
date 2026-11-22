export interface GridPosition {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * GridConfig describes how grid units become pixels. The two layout modes
 * differ only in where a row's height comes from: `fit` divides the container
 * height by the row count so the whole dashboard is on screen, `scroll` gives
 * every row a fixed pixel height and lets the grid run past the fold. Column
 * width scales with the container in both.
 */
export interface GridConfig {
  rows: number;
  cols: number;
  containerWidth: number;
  containerHeight: number;
  gap?: number;
  padding?: number;
  mode?: 'fit' | 'scroll';
  rowHeight?: number;
}

/** DEFAULT_ROW_HEIGHT matches the server's default for a scrolling dashboard. */
export const DEFAULT_ROW_HEIGHT = 80;

export function cellSize(config: GridConfig): { w: number; h: number } {
  const gap = config.gap ?? 8;
  const padding = config.padding ?? 8;
  const w = (config.containerWidth - 2 * padding - gap * (config.cols - 1)) / config.cols;
  if (config.mode === 'scroll') {
    return { w, h: config.rowHeight || DEFAULT_ROW_HEIGHT };
  }
  const h = (config.containerHeight - 2 * padding - gap * (config.rows - 1)) / config.rows;
  return { w, h };
}

/**
 * widgetRect places a position within a region whose row 0 starts at originTop.
 * A fit dashboard has one region starting at the outer padding; a scrolling one
 * has a region per section, each starting below the one above it.
 */
export function widgetRect(
  pos: GridPosition,
  config: GridConfig,
  originTop: number
): { left: number; top: number; width: number; height: number } {
  const gap = config.gap ?? 8;
  const padding = config.padding ?? 8;
  const cell = cellSize(config);

  return {
    left: padding + pos.x * (cell.w + gap),
    top: originTop + pos.y * (cell.h + gap),
    width: pos.width * cell.w + (pos.width - 1) * gap,
    height: pos.height * cell.h + (pos.height - 1) * gap,
  };
}

export function gridToPixels(
  pos: GridPosition,
  config: GridConfig
): { left: number; top: number; width: number; height: number } {
  return widgetRect(pos, config, config.padding ?? 8);
}

/**
 * pixelsToGrid snaps a pixel offset back to grid units. originTop names the top
 * of the region the pixels were measured in; the row is clamped at zero but not
 * at the top, since a scrolling region has no last row.
 */
export function pixelsToGrid(
  px: { left: number; top: number },
  config: GridConfig,
  originTop?: number
): { x: number; y: number } {
  const gap = config.gap ?? 8;
  const padding = config.padding ?? 8;
  const origin = originTop ?? padding;
  const cell = cellSize(config);
  const x = Math.round((px.left - padding) / (cell.w + gap));
  const y = Math.round((px.top - origin) / (cell.h + gap));
  return {
    x: Math.max(0, Math.min(x, config.cols - 1)),
    y: config.mode === 'scroll' ? Math.max(0, y) : Math.max(0, Math.min(y, config.rows - 1)),
  };
}

export function checkCollision(a: GridPosition, b: GridPosition): boolean {
  return !(
    a.x + a.width <= b.x ||
    b.x + b.width <= a.x ||
    a.y + a.height <= b.y ||
    b.y + b.height <= a.y
  );
}

export function hasAnyCollision(
  pos: GridPosition,
  others: GridPosition[],
  excludeIndex?: number
): boolean {
  return others.some(
    (other, i) => i !== excludeIndex && checkCollision(pos, other)
  );
}

/**
 * isWithinBounds checks a placement against the grid. A scrolling dashboard is
 * bounded left and right only — growing downwards is the point of it.
 */
export function isWithinBounds(pos: GridPosition, config: GridConfig): boolean {
  const withinColumns = pos.x >= 0 && pos.x + pos.width <= config.cols;
  if (config.mode === 'scroll') {
    return withinColumns && pos.y >= 0;
  }
  return withinColumns && pos.y >= 0 && pos.y + pos.height <= config.rows;
}

/** occupiedRows reports how many rows a set of placements spans. */
export function occupiedRows(positions: GridPosition[]): number {
  return positions.reduce((rows, p) => Math.max(rows, p.y + p.height), 0);
}

/**
 * findFreePosition looks for room for a widget, shrinking it if the requested
 * size does not fit. A scrolling grid always has room below what is placed, so
 * the search there ends one screen past the last widget rather than failing.
 */
export function findFreePosition(
  width: number,
  height: number,
  occupied: GridPosition[],
  config: GridConfig
): GridPosition | null {
  const scroll = config.mode === 'scroll';
  const limit = scroll ? occupiedRows(occupied) + height : config.rows;

  // Try the requested size first, then progressively shrink to 1x1.
  for (let h = height; h >= 1; h--) {
    for (let w = width; w >= 1; w--) {
      for (let y = 0; y <= limit - h; y++) {
        for (let x = 0; x <= config.cols - w; x++) {
          const candidate: GridPosition = { x, y, width: w, height: h };
          if (!hasAnyCollision(candidate, occupied)) {
            return candidate;
          }
        }
      }
    }
  }
  // A scrolling grid can always append below everything already placed.
  if (scroll) {
    return { x: 0, y: occupiedRows(occupied), width, height };
  }
  return null;
}
