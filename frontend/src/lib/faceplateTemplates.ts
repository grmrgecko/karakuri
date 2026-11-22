// faceplateTemplates are the templates that ship with the editor. They are
// written against the same window.karakuri interface an author's template
// gets, and picking one copies its HTML into the control, so a flow carries
// everything it draws.

export interface BundledTemplate {
  id: string;
  label: string;
  // type is the control value type the template edits.
  type: 'array' | 'string';
  html: string;
}

// curve edits an array of {x, y} points: sensor reading against output. The
// first entity reading on the faceplate, such as a light sensor, is marked on
// the x axis so the author can see where the room is now.
const curve = `<style>
  svg { width: 100%; height: 220px; display: block; touch-action: none; }
  .axis { stroke: var(--theme-surface1, #45475a); }
  .line { fill: none; stroke: var(--theme-accent, #89b4fa); stroke-width: 2; }
  .pt { fill: var(--theme-accent, #89b4fa); cursor: grab; }
  .now { stroke: var(--theme-warning, #f9e2af); stroke-dasharray: 4 3; }
  .lbl { fill: var(--theme-subtext0, #a6adc8); font-size: 11px; }
  .row { display: flex; gap: 8px; align-items: center; font-size: 12px; margin-top: 4px; color: var(--theme-subtext0, #a6adc8); }
  button { font: inherit; color: inherit; background: var(--theme-surface0, #313244); border: 1px solid var(--theme-surface1, #45475a); border-radius: 4px; padding: 2px 8px; cursor: pointer; }
</style>
<svg id="c" viewBox="0 0 400 220"></svg>
<div class="row"><button id="add">Add point</button><button id="del">Remove last</button><span id="info"></span></div>
<script>
  const W = 400, H = 220, P = 28;
  const svg = document.getElementById('c');
  let pts = [];
  let drag = -1;
  const xmax = () => Math.max(100, ...pts.map((p) => p.x));
  const ymax = () => Math.max(100, ...pts.map((p) => p.y));
  const sx = (x) => P + (x / xmax()) * (W - 2 * P);
  const sy = (y) => H - P - (y / ymax()) * (H - 2 * P);
  function now() {
    for (const tab of Object.values(karakuri.readings || {})) for (const r of Object.values(tab)) {
      const n = parseFloat(r && r.state);
      if (!isNaN(n)) return { value: n, unit: r.unit || '' };
    }
    return null;
  }
  function draw() {
    pts.sort((a, b) => a.x - b.x);
    let s = '<line class="axis" x1="' + P + '" y1="' + (H - P) + '" x2="' + (W - P) + '" y2="' + (H - P) + '"/>' +
      '<line class="axis" x1="' + P + '" y1="' + P + '" x2="' + P + '" y2="' + (H - P) + '"/>' +
      '<text class="lbl" x="' + (W - P) + '" y="' + (H - 8) + '" text-anchor="end">' + xmax() + '</text>' +
      '<text class="lbl" x="4" y="' + (P + 4) + '">' + ymax() + '</text>';
    const r = now();
    if (r) s += '<line class="now" x1="' + sx(Math.min(r.value, xmax())) + '" y1="' + P + '" x2="' + sx(Math.min(r.value, xmax())) + '" y2="' + (H - P) + '"/>';
    s += '<polyline class="line" points="' + pts.map((p) => sx(p.x) + ',' + sy(p.y)).join(' ') + '"/>';
    pts.forEach((p, i) => { s += '<circle class="pt" data-i="' + i + '" cx="' + sx(p.x) + '" cy="' + sy(p.y) + '" r="7"/>'; });
    svg.innerHTML = s;
    document.getElementById('info').textContent = r ? 'Now ' + r.value + (r.unit ? ' ' + r.unit : '') : '';
  }
  function toData(e) {
    const b = svg.getBoundingClientRect();
    const x = ((e.clientX - b.left) / b.width) * W, y = ((e.clientY - b.top) / b.height) * H;
    return {
      x: Math.max(0, Math.round(((x - P) / (W - 2 * P)) * xmax())),
      y: Math.max(0, Math.min(ymax(), Math.round(((H - P - y) / (H - 2 * P)) * ymax()))),
    };
  }
  svg.addEventListener('pointerdown', (e) => {
    const i = e.target.dataset && e.target.dataset.i;
    if (i !== undefined) { drag = +i; svg.setPointerCapture(e.pointerId); }
  });
  svg.addEventListener('pointermove', (e) => { if (drag < 0) return; pts[drag] = toData(e); draw(); });
  svg.addEventListener('pointerup', () => { if (drag < 0) return; drag = -1; karakuri.setValue(pts.map((p) => ({ x: p.x, y: p.y }))); });
  document.getElementById('add').onclick = () => {
    const last = pts[pts.length - 1] || { x: 0, y: 0 };
    pts.push({ x: last.x + 50, y: last.y });
    karakuri.setValue(pts.map((p) => ({ x: p.x, y: p.y }))); draw();
  };
  document.getElementById('del').onclick = () => { pts.pop(); karakuri.setValue(pts.map((p) => ({ x: p.x, y: p.y }))); draw(); };
  karakuri.onUpdate((k) => {
    if (drag >= 0) return;
    pts = Array.isArray(k.value) ? k.value.map((p) => ({ x: +p.x || 0, y: +p.y || 0 })) : [{ x: 0, y: 10 }, { x: 100, y: 100 }];
    draw();
  });
</script>`;

// color edits a #rrggbb string with the browser's color picker and a swatch.
const color = `<style>
  .row { display: flex; gap: 10px; align-items: center; }
  input[type=color] { width: 48px; height: 32px; border: none; background: none; padding: 0; }
  code { color: var(--theme-subtext0, #a6adc8); }
</style>
<div class="row"><input id="c" type="color"><code id="v"></code></div>
<script>
  const input = document.getElementById('c');
  input.addEventListener('input', () => { document.getElementById('v').textContent = input.value; karakuri.setValue(input.value); });
  karakuri.onUpdate((k) => {
    const v = typeof k.value === 'string' && /^#[0-9a-f]{6}$/i.test(k.value) ? k.value : '#ffffff';
    input.value = v; document.getElementById('v').textContent = v;
  });
</script>`;

export const bundledTemplates: BundledTemplate[] = [
  { id: 'curve', label: 'Curve editor', type: 'array', html: curve },
  { id: 'color', label: 'Color picker', type: 'string', html: color },
];
