// Port type colors. Edges and their midpoint dots are colored by the payload
// type they carry. The palette is not a hand-kept table: each
// type takes a hue by rotating the wheel in golden-angle steps over the
// canonical type order, so a type registered in the future — a plugin's —
// gets a stable, well-separated color without anyone assigning one.
//
// `any` stays the neutral edge color: an undeclared payload has no type to
// advertise. The red band of the wheel is excluded because red is a semantic
// on the canvas (error branches, invalid edges), so hues are compressed into
// the remainder rather than ever landing on it.

import { PortAny, portTypeList } from './payloadPorts';

// GOLDEN_ANGLE spreads consecutive hues maximally apart, so appending a type
// never crowds an existing color and never shifts one.
const GOLDEN_ANGLE = 137.508;

// HUE_MIN/HUE_MAX bound the usable wheel: raw hues are compressed into
// [30, 330], keeping the red band around 0/360 for error styling.
const HUE_MIN = 30;
const HUE_MAX = 330;

// hueFor maps a type name onto the wheel. Registered types rotate by their
// position in the registry (registration order, served by the backend), so
// appending a type never shifts an existing color; a type the registry has
// not seen hashes its name onto the same wheel, so it keeps one color
// everywhere regardless of load order.
function hueFor(type: string): number {
  // The registry's first entry is "any", which never colors, so positions
  // count from the first colorable type — keeping string at index 0.
  const order = portTypeList().filter((d) => d.name !== PortAny);
  const i = order.findIndex((d) => d.name === type);
  let raw: number;
  if (i >= 0) {
    raw = (i * GOLDEN_ANGLE) % 360;
  } else {
    let h = 0;
    for (let k = 0; k < type.length; k++) h = (h * 31 + type.charCodeAt(k)) >>> 0;
    raw = ((order.length + (h % 97)) * GOLDEN_ANGLE) % 360;
  }
  return HUE_MIN + raw * ((HUE_MAX - HUE_MIN) / 360);
}

// portTypeColor returns the CSS color for a payload type, or undefined for
// `any` and absent types, which keep the neutral edge styling.
export function portTypeColor(type: string | undefined): string | undefined {
  if (!type || type === PortAny) return undefined;
  return `hsl(${hueFor(type).toFixed(1)}deg 65% 70%)`;
}
