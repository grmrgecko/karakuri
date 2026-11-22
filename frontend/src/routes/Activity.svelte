<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { link, navigate } from 'svelte-routing';
  import { api } from '../lib/api/client';
  import { websocket } from '../lib/stores/websocket';
  import { hasPermission, isAuthenticated } from '../lib/stores/auth';
  import PageHeader from '../lib/components/PageHeader.svelte';

  interface Entry {
    id: number;
    at: string;
    last_at: string;
    count: number;
    category: string;
    level: 'info' | 'warning' | 'error';
    subject_kind?: string;
    subject_id?: string;
    subject_name?: string;
    message: string;
    details?: Record<string, unknown>;
    actor_kind: string;
    actor_id?: string;
    actor_name?: string;
    via?: string;
    dashboard_id?: string;
    run_id?: string;
    address?: string;
  }
  interface Page {
    events: Entry[];
    next_before?: number;
  }

  const CATEGORIES = ['device', 'connection', 'flow', 'agent', 'security', 'system', 'custom'];
  const LEVELS = ['info', 'warning', 'error'];
  const RANGES = [
    { value: 'now-1h', label: 'Last hour' },
    { value: 'now-24h', label: 'Last 24 hours' },
    { value: 'now-7d', label: 'Last 7 days' },
    { value: 'now-30d', label: 'Last 30 days' },
    { value: '', label: 'Everything kept' },
  ];

  const canAudit = hasPermission('api:events:audit');
  const categories = CATEGORIES.filter((c) => c !== 'security' || canAudit);

  let entries: Entry[] = $state([]);
  let nextBefore: number | undefined = $state();
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state('');

  let selCategories: string[] = $state([]);
  let selLevels: string[] = $state([]);
  let range = $state('now-24h');
  let text = $state('');
  let subject = $state('');

  let offLive: (() => void)[] = [];
  let searchTimer: ReturnType<typeof setTimeout> | undefined;

  onMount(() => {
    if (!$isAuthenticated) navigate('/login', { replace: true });
    const params = new URLSearchParams(window.location.search);
    subject = params.get('subject') ?? '';
    selLevels = params.getAll('level').filter((l) => LEVELS.includes(l));
    load();
    live();
  });
  onDestroy(() => {
    offLive.forEach((off) => off());
    clearTimeout(searchTimer);
  });

  function query(before?: number): string {
    const p = new URLSearchParams();
    selCategories.forEach((c) => p.append('category', c));
    selLevels.forEach((l) => p.append('level', l));
    if (subject) p.append('subject', subject);
    if (range) p.set('from', range);
    if (text.trim()) p.set('q', text.trim());
    if (before) p.set('before', String(before));
    p.set('limit', '100');
    return `/events?${p.toString()}`;
  }

  // generation counts loads, so a page that arrives after the filters changed
  // is dropped rather than shown under them.
  let generation = 0;

  async function load() {
    const gen = ++generation;
    loading = true;
    error = '';
    try {
      const page = await api.get<Page>(query());
      if (gen !== generation) return;
      entries = page.events;
      nextBefore = page.next_before;
    } catch (e: any) {
      if (gen === generation) error = e.message || 'the event log could not be read';
    } finally {
      if (gen === generation) loading = false;
    }
  }

  async function more() {
    if (!nextBefore) return;
    const gen = generation;
    loadingMore = true;
    try {
      const page = await api.get<Page>(query(nextBefore));
      if (gen !== generation) return;
      // A live update can already have listed an older entry.
      const listed = new Set(entries.map((e) => e.id));
      entries = [...entries, ...page.events.filter((e) => !listed.has(e.id))];
      nextBefore = page.next_before;
    } catch (e: any) {
      if (gen === generation) error = e.message || 'the event log could not be read';
    } finally {
      loadingMore = false;
    }
  }

  // matches applies the filters to a live entry, so the list only grows with
  // entries the current filters would have listed.
  function matches(e: Entry): boolean {
    if (selCategories.length && !selCategories.includes(e.category)) return false;
    if (selLevels.length && !selLevels.includes(e.level)) return false;
    if (subject && `${e.subject_kind}:${e.subject_id}` !== subject) return false;
    const q = text.trim().toLowerCase();
    if (q && ![e.message, e.subject_name, e.actor_name].some((s) => s?.toLowerCase().includes(q))) return false;
    return true;
  }

  function onLive(data: unknown) {
    const e = data as Entry;
    if (!e || typeof e.id !== 'number') return;
    const i = entries.findIndex((x) => x.id === e.id);
    if (i >= 0) {
      entries[i] = e;
    } else if (matches(e)) {
      entries = [e, ...entries];
    }
  }

  function live() {
    offLive.push(websocket.subscribeTopic('events', onLive));
    if (canAudit) offLive.push(websocket.subscribeTopic('events:security', onLive));
  }

  function toggle(list: string[], value: string): string[] {
    return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
  }

  function refilter() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(load, 250);
  }

  function when(s: string): string {
    const d = new Date(s);
    const sameDay = d.toDateString() === new Date().toDateString();
    return d.toLocaleString(undefined, sameDay
      ? { hour: '2-digit', minute: '2-digit', second: '2-digit' }
      : { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  function subjectHref(e: Entry): string | null {
    switch (e.subject_kind) {
      case 'flow': return `/flows/${e.subject_id}`;
      case 'control': return '/settings/controls';
      case 'device': return '/settings/mqtt-devices';
      case 'connection': return '/settings/connections';
      case 'agent': return '/settings/agents';
      case 'backup': return '/settings/backup';
      case 'user': return e.subject_id ? '/settings/users' : null;
    }
    return null;
  }

  function actorText(e: Entry): string {
    switch (e.actor_kind) {
      case 'system': return e.actor_name && e.actor_name !== 'karakuri' ? `${e.actor_name} (command line)` : 'karakuri';
      case 'anonymous': return e.address ? `someone at ${e.address}` : 'someone signed out';
      case 'flow': return `flow ${e.actor_name}`;
      case 'apikey': return `API key ${e.actor_name}`;
      case 'agent': return `agent ${e.actor_name}`;
    }
    let s = e.actor_name || e.actor_kind;
    if (e.via === 'dashboard') s += ' on a dashboard';
    else if (e.via === 'mcp') s += ' through MCP';
    return s;
  }

  function filterTo(e: Entry) {
    if (!e.subject_kind || !e.subject_id) return;
    subject = `${e.subject_kind}:${e.subject_id}`;
    load();
  }
</script>

<div class="activity">
  <PageHeader title="Activity" />

  {#if error}
    <div class="mb-4 p-3 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{error}</div>
  {/if}

  <div class="filters card p-3 mb-4">
    <div class="chips">
      {#each categories as c (c)}
        <button class="chip" class:on={selCategories.includes(c)} onclick={() => { selCategories = toggle(selCategories, c); load(); }}>{c}</button>
      {/each}
    </div>
    <div class="chips">
      {#each LEVELS as l (l)}
        <button class="chip level-{l}" class:on={selLevels.includes(l)} onclick={() => { selLevels = toggle(selLevels, l); load(); }}>{l}</button>
      {/each}
    </div>
    <div class="row">
      <select class="input" bind:value={range} onchange={load} aria-label="Time range">
        {#each RANGES as r (r.value)}<option value={r.value}>{r.label}</option>{/each}
      </select>
      <input class="input grow" placeholder="Search messages, names, and people" bind:value={text} oninput={refilter} aria-label="Search" />
      {#if subject}
        <button class="chip on" onclick={() => { subject = ''; load(); }} title="Show every subject">
          {entries.find((e) => `${e.subject_kind}:${e.subject_id}` === subject)?.subject_name ?? subject} ×
        </button>
      {/if}
    </div>
  </div>

  {#if loading}
    <p class="opacity-60">Loading activity...</p>
  {:else if entries.length === 0}
    <p class="opacity-60 text-center py-8">Nothing matches. Widen the time range or clear a filter.</p>
  {:else}
    <ol class="log card">
      {#each entries as e (e.id)}
        <li class="entry level-{e.level}">
          <span class="time" title={new Date(e.at).toLocaleString()}>{when(e.last_at)}</span>
          <span class="cat">{e.category}</span>
          <div class="body">
            <div class="msg">
              {e.message}
              {#if e.count > 1}<span class="count" title="Repeated from {when(e.at)}">×{e.count}</span>{/if}
            </div>
            <div class="meta">
              {actorText(e)}
              {#if e.subject_kind && e.subject_id}
                · <button class="linkish" onclick={() => filterTo(e)} title="Show only {e.subject_name}">{e.subject_name || e.subject_id}</button>
                {#if subjectHref(e)}<a href={subjectHref(e)} use:link class="opacity-60" title="Open"><i class="las la-external-link-alt"></i></a>{/if}
              {/if}
              {#if e.run_id && e.actor_kind === 'flow'}
                · <a href="/flows/{e.actor_id}" use:link>run {e.run_id.slice(0, 8)}</a>
              {/if}
            </div>
          </div>
        </li>
      {/each}
    </ol>
    {#if nextBefore}
      <div class="text-center mt-3">
        <button class="btn text-sm" onclick={more} disabled={loadingMore}>{loadingMore ? 'Loading...' : 'Older entries'}</button>
      </div>
    {/if}
  {/if}
</div>

<style>
  .activity {
    width: 100%;
    max-width: 72rem;
    margin: 0 auto;
    padding: 1.5rem;
    box-sizing: border-box;
  }
  .filters {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .chips,
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    align-items: center;
  }
  .grow {
    flex: 1;
    min-width: 12rem;
  }
  .chip {
    font-size: 0.75rem;
    padding: 0.15rem 0.6rem;
    border-radius: 999px;
    border: 1px solid var(--theme-surface1);
    text-transform: capitalize;
  }
  .chip.on {
    background-color: var(--theme-accent);
    color: var(--theme-base);
    border-color: var(--theme-accent);
  }
  .log {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .entry {
    display: grid;
    grid-template-columns: 6.5rem 6rem 1fr;
    gap: 0.75rem;
    padding: 0.55rem 1rem;
    border-top: 1px solid var(--theme-surface1);
    border-left: 3px solid transparent;
    font-size: 0.875rem;
  }
  .entry:first-child {
    border-top: none;
  }
  .entry.level-warning {
    border-left-color: var(--theme-warning, #f9e2af);
  }
  .entry.level-error {
    border-left-color: var(--theme-error, #f38ba8);
  }
  .time {
    opacity: 0.7;
    font-variant-numeric: tabular-nums;
    font-size: 0.8rem;
  }
  .cat {
    font-size: 0.7rem;
    text-transform: uppercase;
    opacity: 0.6;
    letter-spacing: 0.03em;
    padding-top: 0.1rem;
  }
  .msg {
    word-break: break-word;
  }
  .count {
    font-size: 0.7rem;
    margin-left: 0.4rem;
    padding: 0 0.35rem;
    border-radius: 3px;
    background-color: var(--theme-surface1);
  }
  .meta {
    font-size: 0.75rem;
    opacity: 0.7;
    margin-top: 0.1rem;
  }
  .linkish {
    text-decoration: underline;
    text-decoration-style: dotted;
  }
  .input {
    width: 100%;
  }
  @media (max-width: 640px) {
    .entry {
      grid-template-columns: 1fr;
      gap: 0.15rem;
    }
  }
</style>
