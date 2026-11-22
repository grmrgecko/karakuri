// faceplateSandbox runs a faceplate's author-written code away from the page.
// Templates and change scripts are written by flow editors and run in the
// browser of whoever opens the faceplate, including people shared the flow at
// faceplate level, so neither may reach the page, its session, or the
// network. Each runs in an iframe sandboxed without same-origin access, under
// a content security policy that refuses every fetch, and talks to the page
// only through postMessage.

// csp refuses network access, frames, and forms; inline script and style are
// the code being sandboxed.
const csp =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:";

// sandboxAttrs lets the frame run script and nothing else: no same-origin
// access, no navigation, no popups, no forms.
export const sandboxAttrs = 'allow-scripts';

// themeVars copies the page's theme into a frame so a template can match it.
export function themeVars(): Record<string, string> {
  const style = getComputedStyle(document.documentElement);
  const out: Record<string, string> = {};
  for (const name of ['base', 'mantle', 'surface0', 'surface1', 'text', 'subtext0', 'accent', 'error', 'warning', 'success']) {
    const v = style.getPropertyValue(`--theme-${name}`).trim();
    if (v) out[name] = v;
  }
  return out;
}

// templateBootstrap is the API a template gets as window.karakuri:
//   karakuri.value, karakuri.control, karakuri.values, karakuri.reading,
//   karakuri.readings (every entity reading by tab and control), karakuri.theme
//   karakuri.onUpdate(fn)   called with the state after any change
//   karakuri.setValue(v)    sets this control's value
const templateBootstrap = `
<script>
(() => {
  const listeners = [];
  const k = { value: undefined, control: {}, values: {}, reading: null, readings: {}, theme: {} };
  k.onUpdate = (fn) => { listeners.push(fn); };
  k.setValue = (value) => { k.value = value; parent.postMessage({ karakuri: 'set', value }, '*'); };
  window.karakuri = k;
  window.addEventListener('message', (e) => {
    const m = e.data;
    if (!m || m.karakuri !== 'state') return;
    Object.assign(k, m.state);
    const root = document.documentElement;
    for (const [name, v] of Object.entries(k.theme || {})) root.style.setProperty('--theme-' + name, v);
    for (const fn of listeners) { try { fn(k); } catch (err) { parent.postMessage({ karakuri: 'error', message: String(err && err.message || err) }, '*'); } }
  });
  window.addEventListener('error', (e) => parent.postMessage({ karakuri: 'error', message: e.message }, '*'));
  const ro = new ResizeObserver(() => parent.postMessage({ karakuri: 'height', height: document.documentElement.scrollHeight }, '*'));
  addEventListener('DOMContentLoaded', () => { ro.observe(document.documentElement); parent.postMessage({ karakuri: 'ready' }, '*'); });
})();
</script>
<style>
  html, body { margin: 0; background: transparent; color: var(--theme-text, #cdd6f4); font: 14px system-ui, sans-serif; }
</style>`;

// templateDocument wraps a template's HTML in the policy and bootstrap.
export function templateDocument(template: string): string {
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}">${templateBootstrap}</head><body>${template}</body></html>`;
}

// TemplateState is what a template is told about its control.
export interface TemplateState {
  value: unknown;
  control: unknown;
  values: Record<string, Record<string, unknown>>;
  reading: unknown;
  readings: Record<string, Record<string, unknown>>;
  theme: Record<string, string>;
}

// ScriptSet is one value a change script asks to set.
export interface ScriptSet {
  tab: string;
  control: string;
  value: unknown;
}

// ChangeEvent describes one control's new value to the change scripts.
export interface ChangeEvent {
  tab: string;
  control: string;
  value: unknown;
  old: unknown;
}

// runnerDocument hosts every faceplate's change script. Each script gets a
// faceplate object:
//   faceplate.onChange(fn)          fn(change) on any faceplate change
//   faceplate.get(tab, control)     the current value
//   faceplate.set(tab, control, v)  set a value, which may trigger more changes
// tab is the faceplate's tab name or key.
function runnerDocument(scripts: { key: string; name: string; source: string }[]): string {
  const bodies = scripts
    .map((s) => `register(${JSON.stringify(s.key)}, ${JSON.stringify(s.name)}, function (faceplate) {\n${s.source}\n});`)
    .join('\n');
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"><script>
(() => {
  const handlers = [];
  let values = {}, names = {}, sets = [];
  const resolve = (tab) => names[tab] || tab;
  function register(key, name, init) {
    const api = {
      onChange: (fn) => handlers.push({ key, fn }),
      get: (tab, control) => (values[resolve(tab)] || {})[control],
      set: (tab, control, value) => {
        const k = resolve(tab);
        (values[k] = values[k] || {})[control] = value;
        sets.push({ tab: k, control, value });
      },
    };
    try { init(api); } catch (err) { parent.postMessage({ karakuri: 'error', key, message: String(err && err.message || err) }, '*'); }
  }
  window.addEventListener('message', (e) => {
    const m = e.data;
    if (!m || m.karakuri !== 'change') return;
    values = m.values; names = m.names; sets = [];
    const errors = [];
    for (const h of handlers) {
      try { h.fn(m.change); } catch (err) { errors.push({ key: h.key, message: String(err && err.message || err) }); }
    }
    parent.postMessage({ karakuri: 'sets', id: m.id, sets, errors }, '*');
  });
  window.register = register;
})();
</script><script>
${bodies}
</script></head><body></body></html>`;
}

// ScriptRunner sends changes to the change scripts and collects the values
// they set. It answers one change at a time.
export class ScriptRunner {
  private frame: HTMLIFrameElement | null = null;
  private pending = new Map<number, (r: { sets: ScriptSet[]; errors: { key: string; message: string }[] }) => void>();
  private next = 1;
  private ready: Promise<void>;
  private onError: (key: string, message: string) => void;

  constructor(scripts: { key: string; name: string; source: string }[], onError: (key: string, message: string) => void) {
    this.onError = onError;
    this.ready = new Promise((resolve) => {
      if (scripts.length === 0) {
        resolve();
        return;
      }
      const frame = document.createElement('iframe');
      frame.setAttribute('sandbox', sandboxAttrs);
      frame.style.display = 'none';
      frame.srcdoc = runnerDocument(scripts);
      frame.addEventListener('load', () => resolve());
      document.body.appendChild(frame);
      this.frame = frame;
    });
    window.addEventListener('message', this.listen);
  }

  private listen = (e: MessageEvent) => {
    if (!this.frame || e.source !== this.frame.contentWindow) return;
    const m = e.data;
    if (m?.karakuri === 'error') this.onError(m.key, m.message);
    if (m?.karakuri === 'sets') {
      const done = this.pending.get(m.id);
      this.pending.delete(m.id);
      done?.({ sets: m.sets ?? [], errors: m.errors ?? [] });
    }
  };

  // change runs every script for one change and returns what they set. A
  // script that does not answer within a second is treated as setting nothing.
  async change(
    change: ChangeEvent,
    values: Record<string, Record<string, unknown>>,
    names: Record<string, string>,
  ): Promise<{ sets: ScriptSet[]; errors: { key: string; message: string }[] }> {
    await this.ready;
    if (!this.frame?.contentWindow) return { sets: [], errors: [] };
    const id = this.next++;
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        resolve({ sets: [], errors: [{ key: change.tab, message: 'the change script did not finish within a second' }] });
      }, 1000);
      this.pending.set(id, (r) => {
        clearTimeout(timer);
        resolve(r);
      });
      this.frame!.contentWindow!.postMessage({ karakuri: 'change', id, change, values: JSON.parse(JSON.stringify(values)), names }, '*');
    });
  }

  destroy() {
    window.removeEventListener('message', this.listen);
    this.frame?.remove();
    this.frame = null;
  }
}

// maxScriptRounds bounds how many times scripts may answer each other's
// changes, so two scripts that set each other cannot hang the page.
export const maxScriptRounds = 10;
