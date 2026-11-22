<svelte:options customElement="activity-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { accepts, actorText, ago, eventsPath, parseConfig, subjectOf, type Entry } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));
  let subjects = $derived(cfg.rows.map(subjectOf).filter(Boolean));

  let entries = $state<Entry[]>([]);
  let loaded = $state(false);
  let error = $state("");
  let now = $state(Date.now());

  let generation = 0;
  let offs: (() => void)[] = [];
  const tick = setInterval(() => (now = Date.now()), 30_000);

  const bridge = () => (window as any).__karakuri;

  let bindKey = $derived(JSON.stringify([subjects, cfg.categories, cfg.levels, cfg.limit]));

  $effect(() => {
    void bindKey;
    untrack(() => void bind());
    return () => unbind();
  });

  onDestroy(() => {
    unbind();
    clearInterval(tick);
  });

  function unbind(): void {
    generation += 1;
    offs.forEach((off) => off());
    offs = [];
  }

  async function bind(): Promise<void> {
    const gen = ++generation;
    entries = [];
    loaded = false;
    error = "";
    if (subjects.length === 0) return;

    // Each subject's entries arrive on its own topic; an update to a folded
    // entry replaces it in place, and a new one goes on top.
    for (const s of subjects) {
      const off = bridge()?.subscribeTopic(`events:subject:${s}`, (data: unknown) => {
        if (gen !== generation) return;
        const e = data as Entry;
        if (!e || typeof e.id !== "number" || !accepts(cfg, e)) return;
        const i = entries.findIndex((x) => x.id === e.id);
        if (i >= 0) entries[i] = e;
        else entries = [e, ...entries].slice(0, cfg.limit);
      });
      if (off) offs.push(off);
    }
    try {
      const res = await bridge().api.get(eventsPath(cfg, subjects));
      if (gen !== generation) return;
      const body = res?.events ? res : (res?.data ?? res);
      entries = Array.isArray(body?.events) ? body.events : [];
      loaded = true;
    } catch (e: any) {
      if (gen !== generation) return;
      error = e?.message || "cannot read the activity";
    }
  }
</script>

<div class="activity">
  {#if subjects.length === 0}
    <p class="hint">Add a control, device, or entity to show its activity.</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if loaded && entries.length === 0}
    <p class="hint">Nothing has happened yet.</p>
  {:else}
    <ul>
      {#each entries as e (e.id)}
        <li class="level-{e.level}">
          <span class="msg">{e.message}{#if e.count > 1}<span class="count">×{e.count}</span>{/if}</span>
          <span class="meta">{ago(e.last_at, now)}{#if cfg.showActor}{" · "}{actorText(e)}{/if}</span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  .activity {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    container-type: size;
  }

  .hint,
  .error {
    margin: auto;
    padding: 0 1em;
    text-align: center;
    font-size: clamp(12px, 5cqmin, 18px);
  }

  .hint {
    opacity: 0.6;
  }

  .error {
    color: var(--theme-error, #f38ba8);
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0.4em 0.6em;
    overflow-y: auto;
    font-size: clamp(11px, 4.2cqmin, 16px);
  }

  li {
    display: flex;
    flex-direction: column;
    gap: 0.1em;
    padding: 0.35em 0 0.35em 0.5em;
    border-left: 3px solid transparent;
    border-bottom: 1px solid var(--theme-border, #45475a);
  }

  li:last-child {
    border-bottom: none;
  }

  li.level-warning {
    border-left-color: var(--theme-warning, #f9e2af);
  }

  li.level-error {
    border-left-color: var(--theme-error, #f38ba8);
  }

  .meta {
    font-size: 0.8em;
    color: var(--theme-subtext, #a6adc8);
  }

  .count {
    margin-left: 0.4em;
    font-size: 0.8em;
    padding: 0 0.3em;
    border-radius: 3px;
    background: var(--theme-surface1, #45475a);
  }
</style>
