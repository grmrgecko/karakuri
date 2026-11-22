import { mount, unmount } from 'svelte';
import { get } from 'svelte/store';
import { websocket } from './stores/websocket';
import { watchLive } from './dashboardLive';
import { api } from './api/client';
import { autoScale, fitText } from './autoScale';
import type { AutoScaleOptions, FitTextOptions } from './autoScale';
import { ICON_FONT_STACK, lineAwesomeIcons } from './icons';
import type { IconEntry } from './icons';
import type { DashboardVariable } from './stores/dashboards';

/**
 * BridgeVariables is what the open dashboard tells widgets about its
 * variables: the resolved definitions, the values this screen holds, and how
 * to change them. The viewer and the editor publish it; widgets and their
 * settings panels read it. setValue changes this screen only.
 */
export interface BridgeVariables {
  dashboardId: string;
  variables: DashboardVariable[];
  values: Record<string, string>;
  setValue?: (name: string, value: string) => void;
}

let variableState: BridgeVariables = { dashboardId: '', variables: [], values: {} };
const variableWatchers = new Set<(state: BridgeVariables) => void>();

/** publishVariables replaces the variables the bridge reports and tells every watcher. */
export function publishVariables(state: BridgeVariables) {
  variableState = state;
  for (const watcher of variableWatchers) watcher(state);
}

// operateState is whether the open dashboard lets this viewer operate what
// it shows. It defaults to true so the editor and other hosts draw controls;
// the viewer publishes the server's answer before its widgets mount.
let operateState = true;

/** publishOperate sets whether widgets should draw controls or only readings. */
export function publishOperate(can: boolean) {
  operateState = can;
}

// WidgetDocumentOptions describes a document a widget hands to the host's
// editor. Widgets have no editor of their own: a config panel lives in a
// narrow sidebar, and the app already ships the full one the flow editor uses.
export interface WidgetDocumentOptions {
  // title names the document in the editor's title bar.
  title: string;
  value: string;
  // format is how the text is read: "json", "yaml", or "plain" for text with
  // no structure. It picks the editor's mode and the save check.
  format?: string;
  saveLabel?: string;
  hint?: string;
  // requireObject rejects a document that does not parse to an object, for
  // the fields holding named parameters rather than a bare value.
  requireObject?: boolean;
  // docs is the id of a reference page to offer in the editor's toolbar, the
  // same button a script field gets. A widget names the page; where the button
  // lives and how it opens is the host's business.
  docs?: string;
}

declare global {
  interface Window {
    __karakuri?: {
      subscribeTopic(topic: string, handler: (data: unknown) => void): () => void;
      // watchDashboardLive follows a dashboard's runtime display state and
      // reads it again after the socket reconnects, which a bare topic
      // subscription cannot do.
      watchDashboardLive(dashboardId: string, handler: (state: unknown) => void): () => void;
      // fetch returns the raw Response for an /api path, with the session and
      // dashboard claim attached, for images and streams.
      fetch(path: string, init?: RequestInit): Promise<Response>;
      api: {
        get<T = unknown>(path: string): Promise<T>;
        post<T = unknown>(path: string, body?: unknown): Promise<T>;
        put<T = unknown>(path: string, body?: unknown): Promise<T>;
        delete<T = unknown>(path: string): Promise<T>;
        upload<T = unknown>(path: string, formData: FormData): Promise<T>;
      };
      autoScale(container: HTMLElement, content: HTMLElement, options?: AutoScaleOptions): () => void;
      fitText(container: HTMLElement, content: HTMLElement, options?: FitTextOptions): () => void;
      editDocument(options: WidgetDocumentOptions): Promise<string | null>;
      parseDocument(text: string, format?: string): Promise<unknown>;
      // icons is the app's own icon pack, so a widget draws from the same set
      // the rest of the UI does instead of falling back to emoji.
      icons(): IconEntry[];
      iconFontStack: string;
      // variables reports the open dashboard's variables and this screen's
      // values; watchVariables follows them, calling back at once.
      variables(): BridgeVariables;
      watchVariables(handler: (state: BridgeVariables) => void): () => void;
      // setVariable changes this screen's value, as its picker would.
      setVariable(name: string, value: string): void;
      // canOperate reports whether this viewer may operate the dashboard.
      // When it is false a widget draws readings instead of controls; the
      // server refuses the press either way.
      canOperate(): boolean;
      // isRenderMode reports whether the page is being photographed for a
      // render, so a widget can skip animation and timers.
      isRenderMode(): boolean;
      // onReconnect calls back each time the live socket reconnects, for
      // widgets that read data the socket does not push.
      onReconnect(handler: () => void): () => void;
    };
  }
}

// The YAML parser is loaded on first use: only a widget that offers YAML pays
// for it, and the app itself never reads YAML.
let yamlModule: typeof import('yaml') | null = null;

async function loadYaml(): Promise<typeof import('yaml')> {
  if (!yamlModule) yamlModule = await import('yaml');
  return yamlModule;
}

// parseText reads a document in the named format. Empty text is no value at
// all rather than an error, which is how an unset optional field reads.
function parseText(text: string, format: string | undefined, yamlMod: typeof import('yaml') | null): unknown {
  if (text.trim() === '') return undefined;
  if (format === 'plain') return text;
  if (format === 'yaml') {
    if (!yamlMod) throw new Error('the YAML parser is not loaded');
    return yamlMod.parse(text);
  }
  return JSON.parse(text);
}

// documentProblem is the editor's save check: text that does not parse, or
// does not hold the object the field needs, is reported in the editor rather
// than dropped when it closes.
function documentProblem(text: string, options: WidgetDocumentOptions, yamlMod: typeof import('yaml') | null): string | null {
  let value: unknown;
  try {
    value = parseText(text, options.format, yamlMod);
  } catch (e: any) {
    return e?.message || 'The document does not parse';
  }
  if (options.requireObject && value !== undefined) {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      return 'This field holds named values, so the document must be an object';
    }
  }
  return null;
}

// openDocumentEditor mounts the flow editor's script editor over the page for
// a widget's config panel and resolves with the saved text, or null when the
// editor is closed without saving.
async function openDocumentEditor(options: WidgetDocumentOptions): Promise<string | null> {
  const format = options.format || 'json';
  const yamlMod = format === 'yaml' ? await loadYaml() : null;
  const { default: ScriptEditor } = await import('./components/editor/ScriptEditor.svelte');

  return new Promise<string | null>((resolve) => {
    const container = document.createElement('div');
    document.body.appendChild(container);

    let saved: string | null = null;
    let component: Record<string, any> | null = null;

    // Tearing the editor down is deferred: onclose fires from inside the
    // component's own save and key handling, and unmounting there would
    // destroy the component while it is still running.
    const close = () => {
      setTimeout(() => {
        if (component) void unmount(component);
        component = null;
        container.remove();
        resolve(saved);
      }, 0);
    };

    component = mount(ScriptEditor, {
      target: container,
      props: {
        title: options.title,
        language: format,
        value: options.value,
        saveLabel: options.saveLabel ?? 'Save',
        hint: options.hint ?? 'Ctrl+Enter saves',
        docs: options.docs ? { guide: options.docs } : null,
        validate: (next: string) => documentProblem(next, { ...options, format }, yamlMod),
        onsave: (next: string) => {
          saved = next;
        },
        onclose: close,
      },
    });
  });
}

// Exposes WebSocket topic subscriptions, the API client, and the host's own
// editor to custom elements that cannot access Svelte stores.
export function initWidgetBridge(): void {
  window.__karakuri = {
    subscribeTopic(topic: string, handler: (data: unknown) => void): () => void {
      return websocket.subscribeTopic(topic, handler);
    },
    watchDashboardLive(dashboardId: string, handler: (state: unknown) => void): () => void {
      return watchLive(dashboardId, (state) => handler(state));
    },
    fetch(path: string, init?: RequestInit): Promise<Response> {
      return api.raw(path, init);
    },
    api: {
      get: <T = unknown>(path: string) => api.get<T>(path),
      post: <T = unknown>(path: string, body?: unknown) => api.post<T>(path, body),
      put: <T = unknown>(path: string, body?: unknown) => api.put<T>(path, body),
      delete: <T = unknown>(path: string) => api.delete<T>(path),
      upload: <T = unknown>(path: string, formData: FormData) => api.upload<T>(path, formData),
    },
    autoScale,
    fitText,
    editDocument: openDocumentEditor,
    // parseDocument reads a widget's stored document. It throws the parser's
    // own complaint, so a widget can report why a press did nothing.
    parseDocument: async (text: string, format?: string) => {
      const yamlMod = format === 'yaml' ? await loadYaml() : null;
      return parseText(text, format, yamlMod);
    },
    // The catalog is read out of the loaded pack's own stylesheet, so a widget
    // offers exactly the icons this build ships and never a stale copy.
    icons: lineAwesomeIcons,
    iconFontStack: ICON_FONT_STACK,
    variables: () => variableState,
    watchVariables(handler: (state: BridgeVariables) => void): () => void {
      variableWatchers.add(handler);
      handler(variableState);
      return () => variableWatchers.delete(handler);
    },
    setVariable(name: string, value: string) {
      variableState.setValue?.(name, value);
    },
    canOperate: () => operateState,
    isRenderMode: () => api.isRenderMode(),
    onReconnect(handler: () => void): () => void {
      let wasConnected = get(websocket).connected;
      return websocket.subscribe((ws) => {
        if (ws.connected && !wasConnected) handler();
        wasConnected = ws.connected;
      });
    },
  };
}
