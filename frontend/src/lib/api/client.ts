import { DASHBOARD_HEADER, dashboardContext } from '../dashboardContext';
import type { NodeFunctionCatalog } from '../stores/nodeCatalog';
import { transientStatus } from './status';

interface RequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
}

interface ApiError {
  code: string;
  message: string;
  details?: Record<string, unknown>;
  // status is the HTTP status of the answer the error came from.
  status?: number;
}

export type ScriptVerificationLanguage = 'javascript' | 'python' | 'bash';

export interface ScriptVerificationRequest {
  language: ScriptVerificationLanguage;
  source: string;
  pure: boolean;
}

export interface ScriptVerificationDiagnostic {
  severity: 'error' | 'warning';
  code: string;
  message: string;
  from: number;
  to: number;
  line: number;
  column: number;
  node_type?: string;
}

export interface ScriptVerificationResult {
  diagnostics: ScriptVerificationDiagnostic[];
}

// MEDIA_COOKIE carries the page's credential to /widget/ uploads. An <img> or
// <video> cannot send an Authorization header, but it does send cookies, and
// the server checks the cookie against the upload's dashboard like any other
// view. It is scoped to that path and holds what local storage already does.
const MEDIA_COOKIE = 'karakuri_media';

function setMediaCookie(token: string | null) {
  if (typeof document === 'undefined') return;
  const secure = window.location.protocol === 'https:' ? '; Secure' : '';
  document.cookie = token
    ? `${MEDIA_COOKIE}=${encodeURIComponent(token)}; Path=/widget/; SameSite=Strict${secure}`
    : `${MEDIA_COOKIE}=; Path=/widget/; Max-Age=0; SameSite=Strict${secure}`;
}

class ApiClient {
  private baseUrl: string;
  private token: string | null = null;
  private renderMode = false;
  // refreshing is the refresh in flight, shared by every caller that needs
  // one at the same moment, such as a request's 401 and the socket's
  // auth_expired.
  private refreshing: Promise<boolean> | null = null;
  private tokenListeners = new Set<(token: string | null) => void>();

  constructor(baseUrl = '/api') {
    this.baseUrl = baseUrl;
    this.token = localStorage.getItem('access_token');

    // Render mode: the headless browser opens a dashboard with a render token
    // in the URL, because a page it navigates to cannot be handed a header.
    // The token is a viewer grant for that one dashboard and lives as long as
    // the render does, so it is used and never stored: a render must not leave
    // a session behind in the profile it ran in.
    if (!this.token && typeof window !== 'undefined') {
      const urlToken = new URLSearchParams(window.location.search).get('token');
      if (urlToken) {
        this.token = urlToken;
        this.renderMode = true;
      }
    }
    setMediaCookie(this.token);
  }

  // onTokenChange follows the access token, so the WebSocket can present a
  // refreshed one without reconnecting.
  onTokenChange(listener: (token: string | null) => void): () => void {
    this.tokenListeners.add(listener);
    return () => this.tokenListeners.delete(listener);
  }

  // isRenderMode reports whether this page is being photographed rather than
  // browsed. Nothing about signing in applies to it.
  isRenderMode(): boolean {
    return this.renderMode;
  }

  setToken(token: string | null) {
    if (this.renderMode) return;
    this.token = token;
    if (token) {
      localStorage.setItem('access_token', token);
    } else {
      localStorage.removeItem('access_token');
    }
    setMediaCookie(token);
    for (const listener of this.tokenListeners) listener(token);
  }

  getToken(): string | null {
    return this.token;
  }

  setRefreshToken(token: string | null) {
    if (token) {
      localStorage.setItem('refresh_token', token);
    } else {
      localStorage.removeItem('refresh_token');
    }
  }

  getRefreshToken(): string | null {
    return localStorage.getItem('refresh_token');
  }

  // Extracts usable data from the standard response envelope.
  // Paginated responses return {data, ...pagination}.
  // All other responses return the data field directly.
  private unwrap(json: any): any {
    if (!json || typeof json !== 'object') return json;

    if (json.meta?.pagination) {
      return { data: json.data, ...json.meta.pagination };
    }

    if ('data' in json) {
      return json.data;
    }

    return json;
  }

  private async request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...this.dashboardHeader(),
      ...opts.headers,
    };

    // Whether this request carried a session at all decides what a 401 means:
    // an expired one, or a gated endpoint an anonymous viewer simply cannot read.
    const hadSession = !!this.token || !!this.getRefreshToken();

    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }

    const response = await fetch(`${this.baseUrl}${path}`, {
      method: opts.method || 'GET',
      headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    });

    if (response.status === 401) {
      const refreshed = await this.tryRefresh();
      if (refreshed) {
        headers['Authorization'] = `Bearer ${this.token}`;
        const retry = await fetch(`${this.baseUrl}${path}`, {
          method: opts.method || 'GET',
          headers,
          body: opts.body ? JSON.stringify(opts.body) : undefined,
        });
        if (!retry.ok) {
          const err = await this.parseError(retry);
          throw err;
        }
        return this.unwrap(await retry.json());
      }
      if (!hadSession || this.renderMode) {
        // Nothing expired: a public dashboard reaching a gated endpoint is a
        // 401 the caller renders, never a reason to send the page to /login.
        // A render is the same case, with nobody to sign in.
        throw await this.parseError(response);
      }
      this.setToken(null);
      this.setRefreshToken(null);
      window.location.href = '/login';
      throw { code: 'UNAUTHORIZED', message: 'Session expired' };
    }

    if (!response.ok) {
      const err = await this.parseError(response);
      throw err;
    }

    if (response.status === 204) {
      return undefined as T;
    }

    return this.unwrap(await response.json());
  }

  // dashboardHeader names the dashboard a request is being made from, when one
  // is on screen. It only ever adds a path to authorization — a caller holding
  // the permission outright never depends on it — so it rides every request
  // rather than being threaded through the calls widgets happen to make.
  private dashboardHeader(): Record<string, string> {
    const id = dashboardContext();
    return id ? { [DASHBOARD_HEADER]: id } : {};
  }

  private async parseError(response: Response): Promise<ApiError> {
    try {
      const json = await response.json();
      return {
        code: json.error || 'UNKNOWN',
        message: json.message || response.statusText,
        details: json.details,
        status: response.status,
      };
    } catch {
      return { code: 'UNKNOWN', message: response.statusText, status: response.status };
    }
  }

  // refreshSession trades the refresh token for a new access token, sharing a
  // refresh already in flight. It reports whether the session is still good.
  refreshSession(): Promise<boolean> {
    if (!this.refreshing) {
      this.refreshing = this.tryRefreshOnce().finally(() => (this.refreshing = null));
    }
    return this.refreshing;
  }

  private tryRefresh(): Promise<boolean> {
    return this.refreshSession();
  }

  private async tryRefreshOnce(): Promise<boolean> {
    if (this.renderMode) return false;
    const refreshToken = this.getRefreshToken();
    if (!refreshToken) return false;

    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
    } catch {
      // A dropped connection says nothing about the session, which is still
      // valid on the server; clearing the tokens here would log the user out
      // and discard the page over a transient failure.
      throw { code: 'NETWORK', message: 'the server is unreachable; check that the service is running' };
    }

    if (!response.ok) {
      // Only the server refusing the token ends the session. A proxy's 502
      // while the service restarts rejects the same way a dropped connection
      // does, and the tokens stay.
      if (transientStatus(response.status)) throw await this.parseError(response);
      return false;
    }

    try {
      const data = this.unwrap(await response.json());
      this.setToken(data.access_token);
      this.setRefreshToken(data.refresh_token);
      return true;
    } catch {
      // The connection dropped while the answer was read.
      throw { code: 'NETWORK', message: 'the server is unreachable; check that the service is running' };
    }
  }

  // fetchFile downloads a server path with the session credential in a header
  // and returns it as a blob URL, for files a browser would otherwise have to
  // be sent to with the token in the address bar, where it lands in history
  // and proxy logs.
  async fetchFile(path: string): Promise<string> {
    const download = async () => {
      const headers: Record<string, string> = {};
      if (this.token) headers['Authorization'] = `Bearer ${this.token}`;
      return fetch(path, { headers });
    };
    let response = await download();
    if (response.status === 401 && (await this.tryRefresh())) {
      response = await download();
    }
    if (!response.ok) throw await this.parseError(response);
    return URL.createObjectURL(await response.blob());
  }

  // raw performs a request under /api and returns the Response itself, for
  // binary and streaming bodies the JSON helpers cannot carry. It sends the
  // same session and dashboard claim, and retries once after a refresh.
  async raw(path: string, init: RequestInit = {}): Promise<Response> {
    const send = () => {
      const headers = new Headers(init.headers);
      for (const [k, v] of Object.entries(this.dashboardHeader())) headers.set(k, v);
      if (this.token) headers.set('Authorization', `Bearer ${this.token}`);
      return fetch(`${this.baseUrl}${path}`, { ...init, headers });
    };
    let response = await send();
    if (response.status === 401 && !this.renderMode && (await this.tryRefresh())) {
      response = await send();
    }
    return response;
  }

  get<T>(path: string) {
    return this.request<T>(path);
  }

  post<T>(path: string, body?: unknown) {
    return this.request<T>(path, { method: 'POST', body });
  }

  put<T>(path: string, body?: unknown) {
    return this.request<T>(path, { method: 'PUT', body });
  }

  delete<T>(path: string) {
    return this.request<T>(path, { method: 'DELETE' });
  }

  // upload posts a FormData body without setting Content-Type so the browser
  // supplies the multipart boundary. Used by dashboard widget uploads.
  async upload<T>(path: string, body: FormData): Promise<T> {
    const doFetch = () => {
      const headers: Record<string, string> = { ...this.dashboardHeader() };
      const token = localStorage.getItem('access_token');
      if (token) headers['Authorization'] = `Bearer ${token}`;
      return fetch(`${this.baseUrl}${path}`, { method: 'POST', headers, body });
    };

    let response = await doFetch();
    if (response.status === 401 && (await this.tryRefresh())) {
      response = await doFetch();
    }

    const json = await response.json();
    if (!response.ok) {
      throw json;
    }
    return this.unwrap(json) as T;
  }

  // streamJSON consumes newline-delimited events without buffering the response.
  async streamJSON(path: string, body: unknown, signal: AbortSignal, onEvent: (event: any) => Promise<void>) {
    const send = () => fetch(`${this.baseUrl}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${this.token}` },
      body: JSON.stringify(body), signal,
    });
    let response = await send();
    if (response.status === 401 && await this.tryRefresh()) response = await send();
    if (!response.ok) throw await this.parseError(response);
    if (!response.body) throw new Error('the server returned no response stream');
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let pending = '';
    try {
      for (;;) {
        const { value, done } = await reader.read();
        pending += decoder.decode(value, { stream: !done });
        let end: number;
        while ((end = pending.indexOf('\n')) >= 0) {
          const line = pending.slice(0, end);
          pending = pending.slice(end + 1);
          if (line.trim()) await onEvent(JSON.parse(line));
        }
        if (done) break;
      }
      if (pending.trim()) await onEvent(JSON.parse(pending));
    } finally {
      await reader.cancel();
      reader.releaseLock();
    }
  }

  // ---- API method groups ----
  // Stores supply the concrete types.

  readonly auth = {
    login: <T>(username: string, password: string) =>
      this.post<T>('/auth/login', { username, password }),
    refresh: <T>(refreshToken: string) =>
      this.post<T>('/auth/refresh', { refresh_token: refreshToken }),
    logout: () => this.post<void>('/auth/logout'),
    me: <T>() => this.get<T>('/auth/me'),
    setPreferences: <T>(preferences: Record<string, unknown>) =>
      this.put<T>('/auth/preferences', { preferences }),
  };

  readonly sessions = {
    list: <T>() => this.get<T>('/auth/sessions'),
    revoke: (id: string) => this.delete<void>(`/auth/sessions/${id}`),
    revokeOthers: <T>() => this.post<T>('/auth/sessions/revoke_others'),
  };

  readonly users = {
    list: <T>(limit = 100, page = 1) => this.get<T>(`/users?limit=${limit}&page=${page}`),
    create: <T>(body: { username: string; email: string; password: string; group_ids?: number[] }) =>
      this.post<T>('/users', body),
    update: <T>(id: string, body: { username?: string; email?: string }) =>
      this.put<T>(`/users/${id}`, body),
    updatePassword: (id: string, body: { old_password?: string; new_password: string }) =>
      this.put<void>(`/users/${id}/password`, body),
    remove: (id: string) => this.delete<void>(`/users/${id}`),
  };

  readonly groups = {
    list: <T>(limit = 100, page = 1) => this.get<T>(`/groups?limit=${limit}&page=${page}`),
    create: <T>(name: string) => this.post<T>('/groups', { name }),
    update: <T>(id: number, name: string) => this.put<T>(`/groups/${id}`, { name }),
    remove: (id: number) => this.delete<void>(`/groups/${id}`),
    listMembers: <T>(id: number) => this.get<T>(`/groups/${id}/members`),
    addMember: <T>(id: number, userId: string) =>
      this.post<T>(`/groups/${id}/members`, { user_id: userId }),
    removeMember: (id: number, userId: string) =>
      this.delete<void>(`/groups/${id}/members/${userId}`),
    listOwners: <T>(id: number) => this.get<T>(`/groups/${id}/owners`),
    addOwner: <T>(id: number, body: { user_id?: string; owner_group_id?: number }) =>
      this.post<T>(`/groups/${id}/owners`, body),
    removeOwner: (id: number, ownerId: number) =>
      this.delete<void>(`/groups/${id}/owners/${ownerId}`),
  };

  readonly roles = {
    list: <T>() => this.get<T>('/roles'),
    create: <T>(name: string, description: string) =>
      this.post<T>('/roles', { name, description }),
    update: <T>(id: number, name: string, description: string) =>
      this.put<T>(`/roles/${id}`, { name, description }),
    remove: (id: number) => this.delete<void>(`/roles/${id}`),
    listPermissions: <T>(roleId: number) => this.get<T>(`/roles/${roleId}/permissions`),
    addPermission: <T>(roleId: number, permission: string) =>
      this.post<T>(`/roles/${roleId}/permissions`, { permission }),
    removePermission: (roleId: number, permId: number) =>
      this.delete<void>(`/roles/${roleId}/permissions/${permId}`),
    listAssignments: <T>() => this.get<T>('/role_assignments'),
    createAssignment: <T>(roleId: number, body: { user_id?: string; group_id?: number }) =>
      this.post<T>('/role_assignments', { role_id: roleId, ...body }),
    removeAssignment: (id: number) => this.delete<void>(`/role_assignments/${id}`),
    permissionTree: <T>() => this.get<T>('/permissions/tree'),
  };

  readonly folders = {
    list: <T>() => this.get<T>('/folders'),
    create: <T>(body: { name: string; parent_id?: number }) => this.post<T>('/folders', body),
    // Replaces both fields: omitting parent_id moves the folder to the root.
    update: <T>(id: number, body: { name: string; parent_id?: number }) =>
      this.put<T>(`/folders/${id}`, body),
    remove: (id: number) => this.delete<void>(`/folders/${id}`),
  };

  readonly flows = {
    // Omitting isSubflow drops the filter, returning flows and subflows together.
    list: <T>(isSubflow?: boolean, limit = 100, page = 1) => {
      const params = new URLSearchParams({ limit: String(limit), page: String(page) });
      if (isSubflow !== undefined) params.set('is_subflow', String(isSubflow));
      return this.get<T>(`/flows?${params}`);
    },
    create: <T>(body: { name: string; description?: string; is_subflow?: boolean; folder_id?: number }) =>
      this.post<T>('/flows', body),
    move: <T>(id: string, folderId: number | null) =>
      this.put<T>(`/flows/${id}/folder`, folderId === null ? {} : { folder_id: folderId }),
    get: <T>(id: string) => this.get<T>(`/flows/${id}`),
    update: <T>(id: string, body: { name?: string; description?: string; error_flow_id?: string }) =>
      this.put<T>(`/flows/${id}`, body),
    remove: (id: string) => this.delete<void>(`/flows/${id}`),
    saveGraph: <T>(id: string, graph: unknown, expected_revision: string) => this.put<T>(`/flows/${id}/graph`, { graph, expected_revision }),
    deploy: <T>(id: string, comment?: string) =>
      this.post<T>(`/flows/${id}/deploy`, comment ? { comment } : {}),
    test: <T>(
      id: string,
      opts?: { triggerNodeId?: string; startNodeId?: string; payload?: unknown; topic?: string },
    ) =>
      this.post<T>(`/flows/${id}/test`, {
        ...(opts?.triggerNodeId ? { trigger_node_id: opts.triggerNodeId } : {}),
        ...(opts?.startNodeId ? { start_node_id: opts.startNodeId } : {}),
        ...(opts?.payload !== undefined ? { payload: opts.payload } : {}),
        ...(opts?.topic ? { topic: opts.topic } : {}),
      }),
    rerun: <T>(id: string) => this.post<T>(`/flows/${id}/rerun`),
    exportDoc: <T>(id: string) => this.get<T>(`/flows/${id}/export`),
    importDoc: <T>(document: unknown) => this.post<T>('/flows/import', { document }),
    restoreVersion: <T>(id: string, version: number) =>
      this.post<T>(`/flows/${id}/versions/${version}/restore`),
    lastRun: <T>(id: string) => this.get<T>(`/flows/${id}/run`),
    promoteSubgraph: <T>(id: string, subgraphId: string, name: string) =>
      this.post<T>(`/flows/${id}/promote_subgraph`, { subgraph_id: subgraphId, name }),
    importSubflow: <T>(id: string, subflowId: string) =>
      this.post<T>(`/flows/${id}/import_subflow`, { subflow_id: subflowId }),
    enable: <T>(id: string) => this.post<T>(`/flows/${id}/enable`),
    disable: <T>(id: string) => this.post<T>(`/flows/${id}/disable`),
    versions: <T>(id: string) => this.get<T>(`/flows/${id}/versions`),
    waits: <T>(id: string) => this.get<T>(`/flows/${id}/waits`),
    cancelWait: (id: string, nodeId: string, key: string) =>
      this.delete<void>(`/flows/${id}/waits/${encodeURIComponent(nodeId)}?key=${encodeURIComponent(key)}`),
  };

  readonly runs = {
    cancel: (id: string) => this.post<void>(`/runs/${id}/cancel`),
  };

  readonly nodeTypes = {
    list: <T>() => this.get<T>('/nodetypes'),
    portTypes: <T>() => this.get<T>('/nodetypes/porttypes'),
    controls: <T>() => this.get<T>('/nodetypes/controls'),
    cronPreview: <T>(expr: string, location: string) =>
      this.get<T>(
        `/nodetypes/cron/preview?expr=${encodeURIComponent(expr)}&location=${encodeURIComponent(location)}`,
      ),
  };

  readonly plugins = {
    list: <T>() => this.get<T>('/plugins'),
  };

  readonly agents = {
    list: <T>() => this.get<T>('/agents'),
    update: <T>(id: string, body: { name: string; description: string; enabled: boolean }) =>
      this.put<T>(`/agents/${id}`, body),
    remove: (id: string) => this.delete<void>(`/agents/${id}`),
    revoke: <T>(id: string) => this.post<T>(`/agents/${id}/revoke`),
    tokens: <T>() => this.get<T>('/agents/tokens'),
    mintToken: <T>(body: { name: string; description: string }) => this.post<T>('/agents/tokens', body),
    deleteToken: (id: string) => this.delete<void>(`/agents/tokens/${id}`),
    ca: <T>() => this.get<T>('/agents/ca'),
    rotateCA: <T>() => this.post<T>('/agents/ca/rotate'),
  };

  readonly guides = {
    // Reference pages rendered from the documentation built into the server.
    list: <T>() => this.get<T>('/guides'),
    get: <T>(id: string) => this.get<T>(`/guides/${id}`),
    // Renders arbitrary markdown — note text — with the same renderer, so the
    // frontend still carries no markdown parser.
    render: <T>(markdown: string) => this.post<T>('/guides/markdown', { markdown }),
  };

  readonly connections = {
    kinds: <T>() => this.get<T>('/connections/kinds'),
    list: <T>() => this.get<T>('/connections'),
    get: <T>(id: string) => this.get<T>(`/connections/${id}`),
    create: <T>(body: { name: string; kind: string; config: Record<string, unknown>; enabled?: boolean }) =>
      this.post<T>('/connections', body),
    update: <T>(
      id: string,
      body: { name?: string; config?: Record<string, unknown>; enabled?: boolean },
    ) => this.put<T>(`/connections/${id}`, body),
    remove: (id: string) => this.delete<void>(`/connections/${id}`),
    test: <T>(id: string) => this.post<T>(`/connections/${id}/test`),
    autoImport: <T>(id: string) => this.get<T>(`/connections/${id}/auto_import`),
    startCodexLogin: <T>() => this.post<T>('/connections/ai/codex/auth/start'),
    pollCodexLogin: <T>(deviceAuthId: string, userCode: string) =>
      this.post<T>('/connections/ai/codex/auth/poll', {
        device_auth_id: deviceAuthId,
        user_code: userCode,
      }),
    haEntities: <T>(id: string) => this.get<T>(`/connections/${id}/ha/entities`),
    haRegistry: <T>(id: string) => this.get<T>(`/connections/${id}/ha/registry`),
    openhabItems: <T>(id: string) => this.get<T>(`/connections/${id}/openhab/items`),
    aiModels: <T>(id: string, refresh = false) =>
      this.get<T>(`/connections/${id}/ai/models${refresh ? '?refresh=true' : ''}`),
  };

  readonly controls = {
    kinds: <T>() => this.get<T>('/controls/kinds'),
    list: <T>() => this.get<T>('/controls'),
    get: <T>(id: string) => this.get<T>(`/controls/${id}`),
    create: <T>(body: {
      name: string;
      kind: string;
      icon?: string;
      options?: Record<string, unknown>;
      allow_public?: boolean;
    }) => this.post<T>('/controls', body),
    update: <T>(
      id: string,
      body: {
        name: string;
        kind: string;
        icon?: string;
        options?: Record<string, unknown>;
        allow_public?: boolean;
      },
    ) => this.put<T>(`/controls/${id}`, body),
    remove: (id: string) => this.delete<void>(`/controls/${id}`),
    // set is the operate path: it takes the value, not the definition.
    set: <T>(id: string, value: unknown, attribute?: string) =>
      this.post<T>(`/controls/${id}/set`, attribute ? { value, attribute } : { value }),
  };

  // A memory store is a named bank the AI nodes remember into: transcripts an
  // agent resumes, facts it recalls, browsed and edited from settings.
  readonly memories = {
    listStores: <T>() => this.get<T>('/memory_stores'),
    createStore: <T>(body: Record<string, unknown>) => this.post<T>('/memory_stores', body),
    updateStore: <T>(id: string, body: Record<string, unknown>) =>
      this.put<T>(`/memory_stores/${id}`, body),
    removeStore: (id: string) => this.delete<void>(`/memory_stores/${id}`),
    // browse lists newest-first without a query and recall-scores with one.
    browse: <T>(id: string, opts: { query?: string; session?: string; kind?: string; limit?: number }) => {
      const params = new URLSearchParams();
      if (opts.query) params.set('query', opts.query);
      if (opts.session) params.set('session', opts.session);
      if (opts.kind) params.set('kind', opts.kind);
      if (opts.limit) params.set('limit', String(opts.limit));
      const qs = params.toString();
      return this.get<T>(`/memory_stores/${id}/memories${qs ? '?' + qs : ''}`);
    },
    createMemory: <T>(id: string, body: Record<string, unknown>) =>
      this.post<T>(`/memory_stores/${id}/memories`, body),
    updateMemory: <T>(id: string, memoryId: string, body: Record<string, unknown>) =>
      this.put<T>(`/memory_stores/${id}/memories/${memoryId}`, body),
    removeMemory: (id: string, memoryId: string) =>
      this.delete<void>(`/memory_stores/${id}/memories/${memoryId}`),
    sessions: <T>(id: string) => this.get<T>(`/memory_stores/${id}/sessions`),
    clearSession: (id: string, session: string) =>
      this.delete<void>(`/memory_stores/${id}/sessions?session=${encodeURIComponent(session)}`),
  };

  // An MQTT device is topics in, a decode script, values out: written by hand,
  // generated from discovery, or an entity another connection reads.
  readonly mqttDevices = {
    list: <T>() => this.get<T>('/mqtt_devices'),
    create: <T>(body: Record<string, unknown>) => this.post<T>('/mqtt_devices', body),
    update: <T>(id: string, body: Record<string, unknown>) =>
      this.put<T>(`/mqtt_devices/${id}`, body),
    remove: (id: string) => this.delete<void>(`/mqtt_devices/${id}`),
    // test runs one of a device's scripts against a sample, so a script is not
    // written blind. It touches nothing that is running.
    test: <T>(body: Record<string, unknown>) => this.post<T>('/mqtt_devices/test', body),
    command: <T>(id: string, body: Record<string, unknown>) =>
      this.post<T>(`/mqtt_devices/${id}/command`, body),
    // Zigbee2MQTT discovery reads the bridge's retained device list; adding
    // creates ordinary devices with generated scripts.
    discoverZigbee2MQTT: <T>(connectionId: string, baseTopic: string) =>
      this.get<T>(
        `/mqtt_devices/zigbee2mqtt?connection_id=${encodeURIComponent(connectionId)}&base_topic=${encodeURIComponent(baseTopic)}`,
      ),
    addZigbee2MQTT: <T>(body: Record<string, unknown>) => this.post<T>('/mqtt_devices/zigbee2mqtt', body),
    regenerate: <T>(id: string) => this.post<T>(`/mqtt_devices/${id}/regenerate`, {}),
    // ESPHome: nodes announcing themselves on the network, a connected
    // node's entities, and adding them as devices.
    esphomeNodes: <T>() => this.get<T>('/connections/esphome/nodes'),
    esphomeEntities: <T>(connectionId: string) =>
      this.get<T>(`/mqtt_devices/esphome?connection_id=${encodeURIComponent(connectionId)}`),
    addESPHome: <T>(body: Record<string, unknown>) => this.post<T>('/mqtt_devices/esphome', body),
    entities: <T>(connectionId: string) =>
      this.get<T>(`/mqtt_devices/entities?connection_id=${encodeURIComponent(connectionId)}`),
    addEntities: <T>(body: Record<string, unknown>) => this.post<T>('/mqtt_devices/entities', body),
    // Home Assistant MQTT discovery: the retained configs on a connection,
    // and adding the chosen ones with generated scripts.
    mqttDiscovery: <T>(connectionId: string) =>
      this.get<T>(`/mqtt_devices/mqtt_discovery?connection_id=${encodeURIComponent(connectionId)}`),
    addMQTTDiscovery: <T>(body: Record<string, unknown>) => this.post<T>('/mqtt_devices/mqtt_discovery', body),
  };

  // A history forward writes control, MQTT device, or Home Assistant values
  // to time-series connections. source narrows the list to the one forward a
  // control or device editor owns: "control:<id>" or "mqtt_device:<id>".
  readonly history = {
    listForwards: <T>(source?: string) =>
      this.get<T>(`/history/forwards${source ? `?source=${encodeURIComponent(source)}` : ''}`),
    createForward: <T>(body: Record<string, unknown>) => this.post<T>('/history/forwards', body),
    updateForward: <T>(id: string, body: Record<string, unknown>) =>
      this.put<T>(`/history/forwards/${id}`, body),
    deleteForward: (id: string) => this.delete<void>(`/history/forwards/${id}`),
    status: <T>() => this.get<T>('/history/status'),
  };

  // Running a script against a made-up message, for the editor's Test button.
  readonly scripts = {
    test: <T>(body: Record<string, unknown>) => this.post<T>('/scripts/test', body),
    nodeFunctions: () => this.get<NodeFunctionCatalog>('/scripts/node_functions'),
    verify: (body: ScriptVerificationRequest) => this.post<ScriptVerificationResult>('/scripts/verify', body),
  };

  readonly renders = {
    listJobs: <T>(dashboardId: string) => this.get<T>(`/dashboards/${dashboardId}/renders`),
    create: <T>(dashboardId: string, body: Record<string, unknown>) =>
      this.post<T>(`/dashboards/${dashboardId}/renders`, body),
    cancel: (dashboardId: string, jobId: string) =>
      this.delete<void>(`/dashboards/${dashboardId}/renders/${jobId}`),
    listSchedules: <T>(dashboardId: string) =>
      this.get<T>(`/dashboards/${dashboardId}/render_schedules`),
    createSchedule: <T>(dashboardId: string, body: Record<string, unknown>) =>
      this.post<T>(`/dashboards/${dashboardId}/render_schedules`, body),
    updateSchedule: <T>(dashboardId: string, schedId: number, body: Record<string, unknown>) =>
      this.put<T>(`/dashboards/${dashboardId}/render_schedules/${schedId}`, body),
    removeSchedule: (dashboardId: string, schedId: number) =>
      this.delete<void>(`/dashboards/${dashboardId}/render_schedules/${schedId}`),
  };

  readonly apiKeys = {
    list: <T>(dashboardId?: string) =>
      this.get<T>(dashboardId ? `/api_keys?dashboard_id=${encodeURIComponent(dashboardId)}` : '/api_keys'),
    create: <T>(body: { name: string; expires_at?: string; permissions?: string[]; dashboard_id?: string }) =>
      this.post<T>('/api_keys', body),
    value: <T>(id: number) => this.get<T>(`/api_keys/${id}/value`),
    permissions: <T>() => this.get<T>('/api_keys/permissions'),
    remove: (id: number) => this.delete<void>(`/api_keys/${id}`),
  };
}

export const api = new ApiClient();
export type { ApiError };
