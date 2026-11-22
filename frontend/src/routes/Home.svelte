<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { link, navigate } from 'svelte-routing';
  import { api } from '../lib/api/client';
  import { auth, hasPermission, isAuthenticated, preferences } from '../lib/stores/auth';
  import { dashboards as dashboardStore } from '../lib/stores/dashboards';
  import { confirm } from '../lib/stores/confirmDialog';
  import { pinnedIds, resolveStart } from '../lib/homeStart';
  import PageHeader from '../lib/components/PageHeader.svelte';
  import { watchLists } from '../lib/liveList';
  import type { ListTopic } from '../lib/liveList';

  interface AttentionItem {
    severity: 'error' | 'warning';
    kind: string;
    subject_id: string;
    subject_name: string;
    message: string;
    count: number;
    at: string;
    recovered?: boolean;
    run_id?: string;
    link: string;
  }
  interface Attention {
    items: AttentionItem[];
    checked: string[];
    unchecked: string[];
    summary: {
      connections_connected?: number;
      connections_total?: number;
      agents_online?: number;
      agents_total?: number;
      flow_failures?: number;
    };
  }
  interface Upcoming {
    kind: 'schedule' | 'wait';
    at: string;
    flow_id: string;
    flow_name: string;
    node_id: string;
    node_label: string;
    key?: string;
    can_cancel?: boolean;
  }
  interface Unfinished {
    id: string;
    name: string;
    updated_at: string;
    deployed: boolean;
  }
  interface ActiveRun {
    id: string;
    flow_id: string;
    flow_name?: string;
    trigger_node_id: string;
    started_at: string;
  }
  interface Problem {
    id: number;
    last_at: string;
    count: number;
    level: string;
    category: string;
    message: string;
  }
  interface DashboardRef {
    id: string;
    name: string;
    description?: string;
  }

  const REFRESH_MS = 30_000;

  // Each section shows only to someone who can read what it lists.
  const canFlows = hasPermission('api:flows:read');
  const canRuns = hasPermission('api:runs:read');
  const canCancelRuns = hasPermission('api:runs:cancel');
  const canDashboards = hasPermission('api:dashboards:read');
  const canEvents = hasPermission('api:events:read');
  const canUnfinished = hasPermission('api:flows:write') || hasPermission('api:dashboards:write');
  const canAttention = ['api:connections:read', 'api:agents:read', 'api:runs:read', 'api:backups:read'].some(hasPermission);

  let ready = $state(false);
  let attention: Attention | null = $state(null);
  let attentionError = $state('');
  let upcoming: Upcoming[] = $state([]);
  let upcomingError = $state('');
  let runs: ActiveRun[] = $state([]);
  let unfinished: { flows?: Unfinished[]; dashboards?: Unfinished[] } = $state({});
  let problems: Problem[] = $state([]);
  let visibleDashboards: DashboardRef[] = $state([]);
  let now = $state(Date.now());

  let pins = $derived(pinnedIds($preferences.pinned_dashboards));
  let pinned = $derived(
    pins.map((id) => visibleDashboards.find((d) => d.id === id)).filter((d): d is DashboardRef => !!d),
  );

  let timer: ReturnType<typeof setInterval> | undefined;
  let stopLive: (() => void) | undefined;

  onMount(async () => {
    if (!$isAuthenticated) {
      navigate('/login', { replace: true });
      return;
    }
    if (canDashboards) {
      try {
        visibleDashboards = await dashboardStore.listAll();
      } catch {
        visibleDashboards = [];
      }
    }
    const start = resolveStart($preferences.start_page, canFlows, visibleDashboards.map((d) => d.id));
    if (start.kind === 'dashboard') {
      navigate(`/dashboard/${start.id}`, { replace: true });
      return;
    }
    if (start.kind === 'list') {
      navigate('/dashboards', { replace: true });
      return;
    }
    ready = true;
    // Runs, list changes, and new problems arrive over the socket. The timer
    // stays for what has no topic of its own, such as backups going overdue.
    const topics: ListTopic[] = [];
    if (canFlows) topics.push('flows');
    if (canDashboards) topics.push('dashboards');
    if (canRuns) topics.push('runs');
    if (canEvents) {
      topics.push({ topic: 'events', when: (ev) => ['warning', 'error'].includes((ev as { level?: string })?.level ?? '') });
    }
    stopLive = watchLists(topics, refresh);
    await refresh();
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') refresh();
    }, REFRESH_MS);
    window.addEventListener('focus', refresh);
  });

  onDestroy(() => {
    clearInterval(timer);
    stopLive?.();
    window.removeEventListener('focus', refresh);
  });

  // refresh reloads every section independently, so one failing endpoint
  // leaves the others current.
  async function refresh() {
    now = Date.now();
    const loads: Promise<void>[] = [];
    if (canAttention) {
      loads.push(api.get<Attention>('/home/attention').then(
        (a) => { attention = a; attentionError = ''; },
        (e) => { attentionError = e.message || 'attention could not be read'; },
      ));
    }
    if (canFlows) {
      loads.push(api.get<{ items: Upcoming[] }>('/home/upcoming').then(
        (u) => { upcoming = u.items; upcomingError = ''; },
        (e) => { upcomingError = e.message || 'upcoming triggers could not be read'; },
      ));
    }
    if (canRuns) {
      loads.push(api.get<ActiveRun[]>('/runs/active').then((r) => { runs = r ?? []; }, () => {}));
    }
    if (canUnfinished) {
      loads.push(api.get<typeof unfinished>('/home/unfinished').then((u) => { unfinished = u; }, () => {}));
    }
    if (canDashboards) {
      // Pinned dashboards follow the list, so one removed or unshared
      // elsewhere leaves the quick launch.
      loads.push(dashboardStore.listAll().then((d) => { visibleDashboards = d; }, () => {}));
    }
    if (canEvents) {
      loads.push(
        api.get<{ events: Problem[] }>('/events?level=warning&level=error&from=now-24h&limit=8')
          .then((p) => { problems = p.events ?? []; }, () => {}),
      );
    }
    await Promise.all(loads);
  }

  async function cancelWait(item: Upcoming) {
    if (!(await confirm(`Cancel the wait in ${item.node_label} on ${item.flow_name}? It will not be delivered.`))) return;
    try {
      await api.delete(`/flows/${item.flow_id}/waits/${encodeURIComponent(item.node_id)}?key=${encodeURIComponent(item.key ?? '')}`);
    } catch {
      // The next refresh shows whether it is still pending.
    }
    refresh();
  }

  async function cancelRun(run: ActiveRun) {
    if (!(await confirm(`Cancel the running ${run.flow_name || 'flow'}?`))) return;
    try {
      await api.post(`/runs/${run.id}/cancel`);
    } catch {
      // The next refresh shows whether it is still running.
    }
    refresh();
  }

  function setPins(ids: string[]) {
    auth.setPreferences({ pinned_dashboards: ids });
  }

  function movePin(id: string, by: number) {
    const ids = pinned.map((d) => d.id);
    const i = ids.indexOf(id);
    const j = i + by;
    if (i < 0 || j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j], ids[i]];
    setPins(ids);
  }

  // allClear names only what was checked, so a user who cannot see agents is
  // not told their agents are fine.
  function allClear(a: Attention): string {
    const parts: string[] = [];
    const s = a.summary;
    if (s.connections_total !== undefined) {
      parts.push(s.connections_total === 0 ? 'no connections' : `${plural(s.connections_total, 'connection')} connected`);
    }
    if (s.agents_total !== undefined && s.agents_total > 0) {
      parts.push(`${plural(s.agents_total, 'agent')} online`);
    }
    if (a.checked.includes('flows')) parts.push('no flow failures in 24 hours');
    if (a.checked.includes('backups')) parts.push('backups current');
    if (parts.length === 0) return 'Nothing needs attention.';
    const text = parts.join(', ');
    return text.charAt(0).toUpperCase() + text.slice(1) + '.';
  }

  function plural(n: number, word: string): string {
    return n === 1 ? `1 ${word}` : `${n} ${word}s`;
  }

  // span is how long ago iso was, without the "ago", for "down for 40 min".
  function span(iso: string): string {
    const m = Math.max(1, Math.round((now - new Date(iso).getTime()) / 60_000));
    if (m < 60) return `${m} min`;
    const h = Math.floor(m / 60);
    if (h < 48) return `${h} h`;
    return `${Math.floor(h / 24)} days`;
  }

  function ago(iso: string): string {
    const d = Math.max(0, now - new Date(iso).getTime());
    const m = Math.round(d / 60_000);
    if (m < 1) return 'just now';
    if (m < 60) return `${m} min ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h} h ago`;
    return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }

  function clock(iso: string): string {
    const d = new Date(iso);
    const sameDay = d.toDateString() === new Date(now).toDateString();
    const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    return sameDay ? time : `${d.toLocaleDateString(undefined, { weekday: 'short' })} ${time}`;
  }

  function until(iso: string): string {
    const m = Math.max(0, Math.round((new Date(iso).getTime() - now) / 60_000));
    if (m < 1) return 'now';
    if (m < 60) return `in ${m} min`;
    const h = Math.floor(m / 60);
    const rest = m % 60;
    return rest ? `in ${h} h ${rest} min` : `in ${h} h`;
  }

  const KIND_ICON: Record<string, string> = {
    connection: 'la-plug',
    agent: 'la-desktop',
    flow: 'la-project-diagram',
    backup: 'la-archive',
    history: 'la-chart-line',
    update: 'la-arrow-circle-up',
  };
</script>

{#if ready}
<div class="home">
  <PageHeader title="Overview" />

  {#if canAttention}
    <section class="card attention" aria-label="Needs attention">
      {#if attentionError}
        <p class="row-line warn"><i class="las la-exclamation-triangle"></i>Could not check what needs attention: {attentionError}</p>
      {:else if !attention}
        <p class="row-line opacity-60">Checking...</p>
      {:else}
        {#if attention.items.length === 0}
          <p class="row-line ok"><i class="las la-check-circle"></i>{allClear(attention)}</p>
        {:else}
          <ol class="items">
            {#each attention.items as item (item.kind + item.subject_id + item.message)}
              <li class="item sev-{item.severity}">
                <i class="las {KIND_ICON[item.kind] ?? 'la-exclamation-circle'} kind" aria-hidden="true"></i>
                <div class="body">
                  <a href={item.link} use:link class="subject">{item.subject_name}</a>
                  <span class="msg" title={item.message}>{item.message.split('\n')[0]}</span>
                  <span class="meta">
                    {#if item.count > 1}{item.count}× · newest {ago(item.at)}{:else if item.kind === 'connection' && item.severity === 'error'}down for {span(item.at)}{:else if item.kind === 'agent'}last seen {ago(item.at)}{:else}{ago(item.at)}{/if}
                    {#if item.recovered} · its latest run succeeded{/if}
                  </span>
                </div>
              </li>
            {/each}
          </ol>
        {/if}
        {#if attention.unchecked.length}
          <p class="row-line warn small"><i class="las la-question-circle"></i>Could not check {attention.unchecked.join(', ')}.</p>
        {/if}
      {/if}
    </section>
  {/if}

  {#if canFlows}
    <PageHeader title="Coming up" level="section" />
    <section class="card list" aria-label="Coming up">
      {#if upcomingError}
        <p class="row-line warn">{upcomingError}</p>
      {:else if upcoming.length === 0}
        <p class="row-line opacity-60">Nothing is scheduled in the next 12 hours.</p>
      {:else}
        <ol>
          {#each upcoming as u (u.kind + u.flow_id + u.node_id + (u.key ?? '') + u.at)}
            <li class="line">
              <span class="when" title={new Date(u.at).toLocaleString()}>{clock(u.at)}</span>
              <span class="rel">{until(u.at)}</span>
              <span class="what">
                <i class="las {u.kind === 'wait' ? 'la-hourglass-half' : 'la-clock'}" aria-hidden="true"></i>
                <a href="/flows/{u.flow_id}" use:link>{u.flow_name}</a>
                <span class="opacity-60">· {u.node_label}{u.kind === 'wait' ? ' deadline' : ''}</span>
              </span>
              {#if u.kind === 'wait' && u.can_cancel}
                <button class="icon-btn" title="Cancel this wait" onclick={() => cancelWait(u)}><i class="las la-times"></i></button>
              {/if}
            </li>
          {/each}
        </ol>
      {/if}
    </section>
  {/if}

  {#if canRuns && runs.length > 0}
    <PageHeader title="Running now" level="section" />
    <section class="card list" aria-label="Running now">
      <ol>
        {#each runs as r (r.id)}
          <li class="line">
            <span class="when">{clock(r.started_at)}</span>
            <span class="rel">{ago(r.started_at)}</span>
            <span class="what">
              <i class="las la-play-circle" aria-hidden="true"></i>
              <a href="/flows/{r.flow_id}" use:link>{r.flow_name || r.flow_id.slice(0, 8)}</a>
            </span>
            {#if canCancelRuns}
              <button class="icon-btn" title="Cancel this run" onclick={() => cancelRun(r)}><i class="las la-stop"></i></button>
            {/if}
          </li>
        {/each}
      </ol>
    </section>
  {/if}

  {#if canUnfinished && ((unfinished.flows?.length ?? 0) + (unfinished.dashboards?.length ?? 0)) > 0}
    <PageHeader title="Unfinished work" level="section" />
    <section class="card list" aria-label="Unfinished work">
      <ol>
        {#each unfinished.flows ?? [] as f (f.id)}
          <li class="line">
            <span class="what">
              <i class="las la-project-diagram" aria-hidden="true"></i>
              <a href="/flows/{f.id}" use:link>{f.name}</a>
              <span class="opacity-60">· {f.deployed ? 'undeployed changes' : 'never deployed'}</span>
            </span>
            <span class="rel">{ago(f.updated_at)}</span>
          </li>
        {/each}
        {#each unfinished.dashboards ?? [] as d (d.id)}
          <li class="line">
            <span class="what">
              <i class="las la-th-large" aria-hidden="true"></i>
              <a href="/dashboard/{d.id}/edit" use:link>{d.name}</a>
              <span class="opacity-60">· {d.deployed ? 'undeployed changes' : 'never deployed'}</span>
            </span>
            <span class="rel">{ago(d.updated_at)}</span>
          </li>
        {/each}
      </ol>
    </section>
  {/if}

  {#if canDashboards}
    <PageHeader title="Quick launch" level="section" />
    {#if pinned.length === 0}
      <p class="card row-line opacity-60">
        <span>Pin dashboards from the <a href="/dashboards" use:link>dashboard list</a> to open them from here.</span>
      </p>
    {:else}
      <div class="tiles">
        {#each pinned as d, i (d.id)}
          <div class="tile card">
            <a href="/dashboard/{d.id}" use:link class="tile-link">
              <i class="las la-th-large" aria-hidden="true"></i>
              <span>{d.name}</span>
            </a>
            <div class="tile-actions">
              <button class="icon-btn" title="Move earlier" disabled={i === 0} onclick={() => movePin(d.id, -1)}><i class="las la-arrow-left"></i></button>
              <button class="icon-btn" title="Move later" disabled={i === pinned.length - 1} onclick={() => movePin(d.id, 1)}><i class="las la-arrow-right"></i></button>
              <button class="icon-btn" title="Unpin" onclick={() => setPins(pinned.filter((p) => p.id !== d.id).map((p) => p.id))}><i class="las la-thumbtack"></i></button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {/if}

  {#if canEvents}
    <PageHeader title="Recent problems" level="section" />
    <section class="card list" aria-label="Recent problems">
      {#if problems.length === 0}
        <p class="row-line opacity-60">No warnings or errors in the last 24 hours.</p>
      {:else}
        <ol>
          {#each problems as p (p.id)}
            <li class="line sev-{p.level}">
              <span class="when">{ago(p.last_at)}</span>
              <span class="rel cat">{p.category}</span>
              <span class="what">
                {p.message.split('\n')[0]}
                {#if p.count > 1}<span class="count">×{p.count}</span>{/if}
              </span>
            </li>
          {/each}
        </ol>
        <p class="row-line small"><a href="/activity?level=warning&level=error" use:link>All warnings and errors in Activity</a></p>
      {/if}
    </section>
  {/if}
</div>
{/if}

<style>
  .home {
    width: 100%;
    max-width: 60rem;
    margin: 0 auto;
    padding: 1.5rem;
    box-sizing: border-box;
  }
  section.card {
    margin-bottom: 1.5rem;
  }
  .row-line {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    margin: 0;
  }
  .row-line.small {
    font-size: 0.8rem;
    padding-top: 0.5rem;
  }
  .row-line i {
    font-size: 1.25rem;
  }
  .ok i {
    color: var(--theme-success, #a6e3a1);
  }
  .warn i {
    color: var(--theme-warning, #f9e2af);
  }
  ol {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .item {
    display: flex;
    gap: 0.75rem;
    align-items: flex-start;
    padding: 0.65rem 1rem;
    border-top: 1px solid var(--theme-surface1);
    border-left: 3px solid transparent;
  }
  .item:first-child,
  .line:first-child {
    border-top: none;
  }
  .sev-error {
    border-left-color: var(--theme-error, #f38ba8);
  }
  .sev-warning {
    border-left-color: var(--theme-warning, #f9e2af);
  }
  .item .kind {
    font-size: 1.3rem;
    opacity: 0.8;
    padding-top: 0.1rem;
  }
  .sev-error .kind {
    color: var(--theme-error, #f38ba8);
  }
  .sev-warning .kind {
    color: var(--theme-warning, #f9e2af);
  }
  .body {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .subject {
    font-weight: 600;
  }
  .msg {
    word-break: break-word;
  }
  .meta {
    font-size: 0.75rem;
    opacity: 0.7;
  }
  .line {
    display: grid;
    grid-template-columns: 5.5rem 7rem 1fr auto;
    gap: 0.75rem;
    align-items: center;
    padding: 0.5rem 1rem;
    border-top: 1px solid var(--theme-surface1);
    border-left: 3px solid transparent;
    font-size: 0.875rem;
  }
  section[aria-label='Unfinished work'] .line {
    grid-template-columns: 1fr auto;
  }
  .when {
    font-variant-numeric: tabular-nums;
  }
  .rel {
    opacity: 0.6;
    font-size: 0.8rem;
  }
  .cat {
    text-transform: uppercase;
    font-size: 0.7rem;
    letter-spacing: 0.03em;
  }
  .what {
    min-width: 0;
    word-break: break-word;
  }
  .count {
    font-size: 0.7rem;
    margin-left: 0.4rem;
    padding: 0 0.35rem;
    border-radius: 3px;
    background-color: var(--theme-surface1);
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr));
    gap: 0.75rem;
    margin-bottom: 1.5rem;
  }
  .tile {
    display: flex;
    flex-direction: column;
  }
  .tile-link {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.4rem;
    padding: 1.25rem 0.75rem 0.5rem;
    text-align: center;
    font-weight: 600;
    flex: 1;
  }
  .tile-link i {
    font-size: 2rem;
    color: var(--theme-accent);
  }
  .tile-actions {
    display: flex;
    justify-content: center;
    gap: 0.25rem;
    padding-bottom: 0.4rem;
    opacity: 0.5;
  }
  .tile:hover .tile-actions,
  .tile:focus-within .tile-actions {
    opacity: 1;
  }
  p.card {
    margin-bottom: 1.5rem;
  }
  @media (max-width: 640px) {
    .home {
      padding: 1rem;
    }
    .line {
      grid-template-columns: 4.5rem 1fr auto;
    }
    .line .rel:not(.cat) {
      display: none;
    }
    .line .cat {
      display: none;
    }
  }
</style>
