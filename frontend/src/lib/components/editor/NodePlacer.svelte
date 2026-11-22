<script lang="ts">
  // A floating node placer opened by double-clicking the canvas or by releasing
  // a connection over empty space. When it opens from a released connection it
  // is filtered to node types that can actually take (or supply) that port's
  // type, so the list only ever offers a node that will connect.
  import { nodeCatalog, isTrigger } from '../../stores/nodeCatalog';
  import { connectionsStore } from '../../stores/connections';
  import type { NodeMeta, PortSpec } from '../../stores/nodeCatalog';
  import {
    compatible,
    messageCompatible,
    portTypeAccepts,
    portTypeLabel,
    PortAny,
  } from '../../utils/payloadPorts';

  // A pending connection the placer should satisfy: the type of the port the
  // drag started from, and which end of the new node has to match it.
  export interface PlacerFilter {
    // portType is the type of the dragged port.
    portType: string;
    // need is the port a candidate node must expose: "input" when the drag
    // started at an output, "output" when it started at an input.
    need: 'input' | 'output';
    // value marks a drag from a parameter port, where the new node supplies a
    // config field over a value edge rather than passing a message on.
    value: boolean;
  }

  let {
    x,
    y,
    mode = 'flow',
    filter = null,
    onpick,
    onclose,
  }: {
    x: number;
    y: number;
    mode?: 'flow' | 'subflow';
    filter?: PlacerFilter | null;
    onpick: (type: string) => void;
    onclose: () => void;
  } = $props();

  let search = $state('');
  let selected = $state(0);
  let input = $state<HTMLInputElement | undefined>();

  $effect(() => {
    input?.focus();
  });

  function hasConnection(kind: string | undefined): boolean {
    if (!kind || !$connectionsStore.loaded) return true;
    return $connectionsStore.connections.some((c) => c.kind === kind);
  }

  // fitsFilter reports whether a candidate port satisfies the dragged one. A
  // value edge supplies a config field of a declared type, so an undeclared
  // output is no good there; the message path takes one, because what a webhook
  // or a script produces is not knowable until the run.
  function fitsFilter(type: string): boolean {
    if (!filter) return true;
    if (filter.need === 'input') return messageCompatible(filter.portType, type);
    return filter.value
      ? compatible(type, filter.portType)
      : messageCompatible(type, filter.portType);
  }

  // fitsPort reports whether a port could satisfy the dragged one. A port typed
  // by a rule is judged on the types the rule can reach, not the untyped
  // fallback it advertises unconfigured: a template can be read as an integer,
  // and an author dragging from a Number parameter is entitled to find it here.
  function fitsPort(port: PortSpec): boolean {
    if (fitsFilter(port.type)) return true;
    const rule = port.type_rule;
    if (!rule) return false;
    return (
      (rule.cases ?? []).some((c) => fitsFilter(c.type)) || fitsFilter(rule.default ?? port.type)
    );
  }

  // matchesFilter reports whether a node type has a port that would accept the
  // dragged connection. Dynamic-port nodes are always offered: their ports come
  // from config that does not exist yet, so filtering them out would hide the
  // very nodes an author is most likely reaching for.
  function matchesFilter(meta: NodeMeta): boolean {
    if (!filter) return true;
    // A value edge pulls its source on demand, so only a pure node can supply
    // one. Offering an impure node here would end in a save the server rejects.
    if (filter.value && !meta.pure) return false;
    if (meta.dynamic_ports) return true;
    const ports = filter.need === 'input' ? meta.inputs : meta.outputs;
    if (!ports || ports.length === 0) return false;
    return ports.some(fitsPort);
  }

  let groups = $derived.by(() => {
    const q = search.trim().toLowerCase();
    return $nodeCatalog.categories
      .map((cat) => ({
        ...cat,
        types: cat.types.filter(
          (m) =>
            m.type !== 'subflow' &&
            (mode === 'subflow' ? !isTrigger(m) : m.category !== 'port') &&
            hasConnection(m.connection_kind) &&
            // Only a node with an input of its own can satisfy a released
            // connection that needs one: triggers and pure sources have none,
            // and a freshly placed node exposes no parameters yet. The test is
            // the ports rather than any taxonomy, because the routing Set and
            // Reroute are wiring and do take an input.
            !(filter?.need === 'input' && (isTrigger(m) || !m.inputs?.length)) &&
            matchesFilter(m) &&
            (!q ||
              m.label.toLowerCase().includes(q) ||
              m.type.toLowerCase().includes(q) ||
              m.description.toLowerCase().includes(q) ||
              // The group is a host's name, so searching it lists everything
              // that runs there.
              cat.group.toLowerCase().includes(q))
        ),
      }))
      .filter((cat) => cat.types.length > 0);
  });

  // flat is the keyboard navigation order across all groups.
  let flat = $derived(groups.flatMap((g) => g.types));

  $effect(() => {
    // Keep the highlight in range as the query narrows the list.
    if (selected >= flat.length) selected = Math.max(0, flat.length - 1);
  });

  function pick(type: string) {
    onpick(type);
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onclose();
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      selected = Math.min(selected + 1, flat.length - 1);
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      selected = Math.max(selected - 1, 0);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (flat[selected]) pick(flat[selected].type);
    }
  }

  // indexOf gives each row its position in the flat keyboard order.
  function indexOf(meta: NodeMeta): number {
    return flat.indexOf(meta);
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- The backdrop closes the placer on any outside click without swallowing
     the canvas underneath for longer than the placer is open. -->
<div
  class="placer-backdrop"
  role="presentation"
  onclick={onclose}
  oncontextmenu={(e) => {
    e.preventDefault();
    onclose();
  }}
></div>

<div class="placer" style="left: {x}px; top: {y}px;" role="dialog" aria-label="Add node">
  <div class="placer-head">
    <input
      bind:this={input}
      bind:value={search}
      class="placer-search"
      placeholder="Search nodes..."
      aria-label="Search nodes"
    />
    {#if filter}
      <span class="placer-filter" title="Only nodes that can connect to this port are listed">
        {filter.need === 'input' ? 'accepts' : 'emits'}
        <strong>{portTypeLabel(filter.portType)}</strong>
        {#if portTypeAccepts(filter.portType)}
          <span>({portTypeAccepts(filter.portType)})</span>
        {/if}
      </span>
    {/if}
  </div>

  <div class="placer-list">
    {#each groups as cat, i (cat.id)}
      <!-- The palette group renders once, above the first folder in it. -->
      {#if i === 0 || groups[i - 1].group !== cat.group}
        <div class="placer-supergroup">{cat.group}</div>
      {/if}
      <div class="placer-group">{cat.label}</div>
      {#each cat.types as meta (meta.type)}
        <button
          type="button"
          class="placer-item"
          class:selected={indexOf(meta) === selected}
          onclick={() => pick(meta.type)}
          onmouseenter={() => (selected = indexOf(meta))}
        >
          <i class="las {meta.icon}"></i>
          <span class="placer-label">{meta.label}</span>
          {#if filter}
            {@const port = (filter.need === 'input' ? meta.inputs : meta.outputs)?.find((p) =>
              fitsFilter(p.type)
            )}
            {#if port && port.type !== PortAny}
              <span class="placer-port">{portTypeLabel(port.type)}</span>
            {/if}
          {/if}
        </button>
      {/each}
    {/each}
    {#if flat.length === 0}
      <p class="placer-empty">
        {#if filter}
          No node accepts {portTypeLabel(filter.portType).toLowerCase()} here.
        {:else}
          No nodes match "{search}".
        {/if}
      </p>
    {/if}
  </div>
</div>

<style>
  .placer-backdrop {
    position: fixed;
    inset: 0;
    z-index: 56;
  }

  .placer {
    position: fixed;
    z-index: 57;
    width: 300px;
    max-height: 380px;
    display: flex;
    flex-direction: column;
    border-radius: var(--theme-radius-md, 8px);
    background-color: var(--theme-mantle, #181825);
    border: 1px solid var(--theme-surface1, #45475a);
    box-shadow: 0 8px 28px rgba(0, 0, 0, 0.5);
    overflow: hidden;
  }

  .placer-head {
    padding: 0.5rem;
    border-bottom: 1px solid var(--theme-surface0, #313244);
  }

  .placer-search {
    width: 100%;
    padding: 0.35rem 0.5rem;
    font-size: 0.8rem;
    border-radius: 4px;
    color: var(--theme-text, #cdd6f4);
    background-color: var(--theme-base, #1e1e2e);
    border: 1px solid var(--theme-surface1, #45475a);
  }

  .placer-search:focus {
    outline: none;
    border-color: var(--theme-accent, #89b4fa);
  }

  .placer-filter {
    display: block;
    margin-top: 0.35rem;
    font-size: 0.65rem;
    color: var(--theme-subtext0, #a6adc8);
  }

  .placer-filter strong {
    color: var(--theme-mauve, #cba6f7);
  }

  .placer-list {
    overflow-y: auto;
    padding: 0.25rem;
  }

  .placer-supergroup {
    padding: 0.45rem 0.5rem 0.1rem;
    font-size: 0.65rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--theme-subtext0, #a6adc8);
  }

  .placer-group {
    padding: 0.35rem 0.5rem 0.15rem 0.85rem;
    font-size: 0.6rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--theme-overlay0, #6c7086);
  }

  .placer-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.35rem 0.5rem 0.35rem 0.85rem;
    border: none;
    border-radius: 4px;
    background: none;
    text-align: left;
    font-size: 0.8rem;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
  }

  .placer-item.selected {
    background-color: var(--theme-surface0, #313244);
  }

  .placer-item i {
    width: 1.1rem;
    text-align: center;
    color: var(--theme-subtext0, #a6adc8);
  }

  .placer-label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .placer-port {
    font-size: 0.6rem;
    font-weight: 600;
    padding: 0 0.25rem;
    border-radius: 3px;
    color: var(--theme-mauve, #cba6f7);
    background-color: color-mix(in srgb, var(--theme-mauve, #cba6f7) 16%, transparent);
  }

  .placer-empty {
    padding: 0.75rem 0.5rem;
    font-size: 0.75rem;
    text-align: center;
    color: var(--theme-subtext0, #a6adc8);
  }
</style>
