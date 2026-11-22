<script lang="ts" module>
  // NVR connections and camera lists are read once per page and shared by
  // every picker on it.
  interface ConnectionEntry {
    id: string;
    name: string;
    kind: string;
  }
  export interface CameraEntry {
    name: string;
    zones: string[];
    objects: string[];
    receiving: boolean;
  }

  // nvrKinds are the connection kinds that serve NVR cameras.
  const nvrKinds = new Set(["frigate"]);

  let connectionsRequest: Promise<ConnectionEntry[]> | null = null;
  const cameraRequests = new Map<string, Promise<CameraEntry[]>>();

  function loadConnections(): Promise<ConnectionEntry[]> {
    if (!connectionsRequest) {
      connectionsRequest = (window as any).__karakuri.api
        .get("/connections")
        .then((res: any) => {
          const list: ConnectionEntry[] = Array.isArray(res) ? res : (res?.data ?? []);
          return list.filter((c) => nvrKinds.has(c.kind));
        })
        .catch(() => {
          connectionsRequest = null;
          return [];
        });
    }
    return connectionsRequest as Promise<ConnectionEntry[]>;
  }

  export function loadCameras(conn: string): Promise<CameraEntry[]> {
    let request = cameraRequests.get(conn);
    if (!request) {
      request = (window as any).__karakuri.api
        .get(`/connections/${encodeURIComponent(conn)}/nvr/cameras`)
        .then((res: any) => (res?.data?.cameras ?? res?.cameras ?? []) as CameraEntry[])
        .catch((e: any) => {
          cameraRequests.delete(conn);
          throw e;
        });
      cameraRequests.set(conn, request as Promise<CameraEntry[]>);
    }
    return request as Promise<CameraEntry[]>;
  }
</script>

<script lang="ts">
  // NVRCameraPicker chooses an NVR connection and one of its cameras, or
  // every camera when allowAll is set.
  let {
    connectionId = "",
    camera = "",
    allowAll = false,
    idPrefix = "nvr",
    onchange,
  }: {
    connectionId?: string;
    camera?: string;
    allowAll?: boolean;
    idPrefix?: string;
    onchange: (patch: { connectionId?: string; camera?: string }) => void;
  } = $props();

  let connections = $state<ConnectionEntry[]>([]);
  let cameras = $state<CameraEntry[]>([]);
  let loadError = $state("");

  $effect(() => {
    void loadConnections().then((list) => (connections = list));
  });

  $effect(() => {
    const conn = connectionId;
    cameras = [];
    loadError = "";
    if (!conn) return;
    loadCameras(conn)
      .then((list) => {
        if (conn === connectionId) cameras = list;
      })
      .catch((e: any) => {
        if (conn === connectionId) loadError = String(e?.message ?? "the camera list did not load");
      });
  });
</script>

<div class="field">
  <label class="label" for="{idPrefix}-conn">NVR</label>
  <select id="{idPrefix}-conn" class="input" value={connectionId}
          onchange={(e: Event) => onchange({ connectionId: (e.target as HTMLSelectElement).value, camera: "" })}>
    <option value="">Choose a connection</option>
    {#each connections as c (c.id)}
      <option value={c.id}>{c.name}</option>
    {/each}
  </select>
  {#if connections.length === 0}
    <span class="hint">Add a Frigate connection in Settings, Connections.</span>
  {/if}
</div>

<div class="field">
  <label class="label" for="{idPrefix}-camera">Camera</label>
  <select id="{idPrefix}-camera" class="input" value={camera} disabled={!connectionId}
          onchange={(e: Event) => onchange({ camera: (e.target as HTMLSelectElement).value })}>
    <option value="">Choose a camera</option>
    {#if allowAll}
      <option value="*">Every camera</option>
    {/if}
    {#each cameras as c (c.name)}
      <option value={c.name}>{c.name}{c.receiving ? "" : " (no frames)"}</option>
    {/each}
    {#if camera && camera !== "*" && !cameras.some((c) => c.name === camera)}
      <option value={camera}>{camera}</option>
    {/if}
  </select>
  {#if loadError}
    <span class="hint error">{loadError}</span>
  {/if}
</div>

<style>
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .hint.error {
    color: var(--theme-error, #f38ba8);
    opacity: 1;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }
</style>
