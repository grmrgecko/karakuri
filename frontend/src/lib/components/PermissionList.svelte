<script lang="ts">
  import Autocomplete from './Autocomplete.svelte';
  import { isTerminalPermission, permissionSuggestions, type PermissionTree } from '../permissionTree';

  // PermissionList edits a list of permission strings: type a path, complete it
  // against a permission tree, and add it; remove one from its row. A role
  // grants its permissions this way and an API key is limited this way, so the
  // two read alike. The owner decides what adding and removing do: a role saves
  // each change, a key form keeps them until it is submitted.
  let {
    permissions,
    tree,
    onadd,
    onremove,
    emptyText = 'No permissions assigned.',
    disabled = false,
  }: {
    permissions: string[];
    tree: PermissionTree;
    onadd: (permission: string) => void | Promise<void>;
    onremove: (permission: string, index: number) => void | Promise<void>;
    emptyText?: string;
    disabled?: boolean;
  } = $props();

  let input = $state('');
  let suggestions: string[] = $state([]);
  let show = $state(false);
  let selected = $state(-1);

  function update() {
    suggestions = permissionSuggestions(tree, input);
    show = suggestions.length > 0;
    selected = -1;
  }

  function choose(suggestion: string) {
    input = suggestion;
    show = false;
    // A level, rather than an action, takes another segment.
    if (!isTerminalPermission(tree, suggestion)) {
      input += ':';
      update();
    }
  }

  async function add() {
    const p = input.trim();
    if (!p || disabled) return;
    if (permissions.includes(p)) {
      input = '';
      return;
    }
    await onadd(p);
    input = '';
    show = false;
  }

  function keydown(e: KeyboardEvent) {
    if (e.key === 'Tab' && show && suggestions.length > 0) {
      e.preventDefault();
      choose(suggestions[selected >= 0 ? selected : 0]);
    } else if (e.key === 'ArrowDown' && show) {
      e.preventDefault();
      selected = Math.min(selected + 1, suggestions.length - 1);
    } else if (e.key === 'ArrowUp' && show) {
      e.preventDefault();
      selected = Math.max(selected - 1, 0);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (show && selected >= 0) choose(suggestions[selected]);
      else void add();
    }
  }
</script>

<div class="permission-list">
  <div class="text-sm p-3 rounded mb-3 hint">
    Type a permission path like <code>api:flows:read</code>. Use <code>*</code> as a wildcard at any level. Press <kbd>Tab</kbd> to autocomplete.
  </div>

  <div class="flex gap-2 mb-3 items-start">
    <Autocomplete show={show && suggestions.length > 0} maxHeight="150px">
      {#snippet children()}
        <input class="input w-full text-sm" placeholder="e.g. api:flows:read" aria-label="Permission"
               bind:value={input} {disabled}
               oninput={update}
               onfocus={update}
               onkeydown={keydown}
               onblur={() => setTimeout(() => (show = false), 200)} />
      {/snippet}
      {#snippet items()}
        {#each suggestions as s, i (s)}
          <div class="autocomplete-item" class:selected={i === selected}
               role="option" tabindex="-1" aria-selected={i === selected}
               onmousedown={(e: MouseEvent) => { e.preventDefault(); choose(s); }}>{s}</div>
        {/each}
      {/snippet}
    </Autocomplete>
    <button type="button" class="btn btn-primary text-sm" onclick={add} disabled={!input.trim() || disabled}>
      Add
    </button>
  </div>

  {#if permissions.length > 0}
    <div class="flex flex-col gap-1">
      {#each permissions as p, i (p)}
        <div class="flex items-center justify-between px-3 py-1.5 rounded text-sm row">
          <code>{p}</code>
          <button type="button" class="btn btn-danger text-xs px-2 py-0.5" onclick={() => onremove(p, i)}
                  aria-label="Remove {p}" {disabled}>
            <i class="las la-times"></i>
          </button>
        </div>
      {/each}
    </div>
  {:else}
    <p class="text-sm opacity-60">{emptyText}</p>
  {/if}
</div>

<style>
  .hint,
  .row {
    background-color: var(--theme-surface0);
  }

  .autocomplete-item {
    padding: 0.4rem 0.6rem;
    cursor: pointer;
    font-size: 0.8rem;
  }

  .autocomplete-item:hover,
  .autocomplete-item.selected {
    background-color: var(--theme-surface1, #45475a);
  }
</style>
