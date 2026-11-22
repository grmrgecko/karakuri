<script lang="ts">
  import { onMount } from 'svelte';
  import { memoryStoresStore, type MemoryStore, type Memory, type MemorySession } from '../../lib/stores/memories';
  import { connectionsStore } from '../../lib/stores/connections';
  import { confirm } from '../../lib/stores/confirmDialog';
  import { hasPermission } from '../../lib/stores/auth';
  import { api } from '../../lib/api/client';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';

  let loading = $state(true);
  let error = $state('');
  let stores = $derived($memoryStoresStore.stores);
  let canWrite = $derived(hasPermission('api:memories:write'));

  // AI connections that can serve embeddings. Anthropic ships no embeddings
  // API, so offering it here would only configure a store that cannot embed;
  // a plugin provider that turns out the same fails with the driver's own
  // message naming the provider.
  let embedConnections = $derived(
    $connectionsStore.connections.filter(
      (c) => c.kind === 'ai' && (c.config as Record<string, unknown>)?.provider !== 'anthropic',
    ),
  );

  // Store create/edit modal state.
  let showStoreModal = $state(false);
  let editingStore: MemoryStore | null = $state(null);
  let savingStore = $state(false);
  let storeError = $state('');
  let storeForm = $state({
    name: '',
    description: '',
    embed_connection: '',
    embed_model: '',
    half_life_days: 30,
    recency_weight: 0.25,
    max_session_turns: 200,
    max_memories: 0,
  });

  // Models the picked embedding connection serves, for the model datalist.
  let embedModels = $state<string[]>([]);

  // Browser state: which store is open, its memories, and the filters.
  let browsing: MemoryStore | null = $state(null);
  let memories = $state<Memory[]>([]);
  let sessions = $state<MemorySession[]>([]);
  let browseLoading = $state(false);
  let query = $state('');
  let kindFilter = $state('');
  let sessionFilter = $state('');
  let searchTimer: ReturnType<typeof setTimeout> | null = null;

  // Memory create/edit modal state.
  let showMemoryModal = $state(false);
  let editingMemory: Memory | null = $state(null);
  let savingMemory = $state(false);
  let memoryError = $state('');
  let memoryForm = $state({
    text: '',
    kind: 'fact',
    session: '',
    role: '',
    tags: '',
    importance: 0.5,
    expires_at: '',
  });

  onMount(async () => {
    try {
      await Promise.all([
        memoryStoresStore.load(),
        connectionsStore.load().catch(() => {}),
      ]);
    } catch (e: any) {
      error = e.message || 'Failed to load memory stores';
    } finally {
      loading = false;
    }
  });

  function openCreateStore() {
    editingStore = null;
    storeError = '';
    storeForm = {
      name: '', description: '', embed_connection: '', embed_model: '',
      half_life_days: 30, recency_weight: 0.25, max_session_turns: 200, max_memories: 0,
    };
    embedModels = [];
    showStoreModal = true;
  }

  function openEditStore(store: MemoryStore) {
    editingStore = store;
    storeError = '';
    storeForm = {
      name: store.name,
      description: store.description || '',
      embed_connection: store.embed_connection || '',
      embed_model: store.embed_model || '',
      half_life_days: store.half_life_days,
      recency_weight: store.recency_weight,
      max_session_turns: store.max_session_turns,
      max_memories: store.max_memories,
    };
    embedModels = [];
    if (storeForm.embed_connection) loadEmbedModels(storeForm.embed_connection);
    showStoreModal = true;
  }

  // loadEmbedModels completes the model field from the connection's catalog,
  // the same list the AI Model node's field offers.
  async function loadEmbedModels(connectionId: string) {
    embedModels = [];
    if (!connectionId) return;
    try {
      embedModels = (await api.connections.aiModels<string[]>(connectionId)) || [];
    } catch {
      // Completion is a convenience; the field still accepts anything typed.
    }
  }

  async function saveStore() {
    storeError = '';
    if (!storeForm.name.trim()) {
      storeError = 'Name is required';
      return;
    }
    if (storeForm.embed_connection && !storeForm.embed_model.trim()) {
      storeError = 'An embedding connection needs an embedding model';
      return;
    }
    savingStore = true;
    try {
      const body = {
        name: storeForm.name.trim(),
        description: storeForm.description.trim(),
        embed_connection: storeForm.embed_connection,
        embed_model: storeForm.embed_model.trim(),
        half_life_days: Number(storeForm.half_life_days) || 0,
        recency_weight: Number(storeForm.recency_weight) || 0,
        max_session_turns: Number(storeForm.max_session_turns) || 0,
        max_memories: Number(storeForm.max_memories) || 0,
      };
      if (editingStore) {
        const updated = await memoryStoresStore.update(editingStore.id, body);
        if (browsing?.id === updated.id) browsing = updated;
      } else {
        await memoryStoresStore.create(body);
      }
      showStoreModal = false;
    } catch (e: any) {
      storeError = e.message || 'Failed to save the memory store';
    } finally {
      savingStore = false;
    }
  }

  async function removeStore(store: MemoryStore) {
    const ok = await confirm(
      `Delete memory store "${store.name}" and everything it remembers? Flows naming it will stop resolving.`,
    );
    if (!ok) return;
    error = '';
    try {
      await memoryStoresStore.remove(store.id);
      if (browsing?.id === store.id) browsing = null;
    } catch (e: any) {
      error = e.message || 'Failed to delete the memory store';
    }
  }

  async function openBrowser(store: MemoryStore) {
    browsing = store;
    query = '';
    kindFilter = '';
    sessionFilter = '';
    await refreshBrowser();
  }

  async function refreshBrowser() {
    if (!browsing) return;
    browseLoading = true;
    try {
      const [list, sess] = await Promise.all([
        api.memories.browse<Memory[]>(browsing.id, {
          query: query.trim(),
          session: sessionFilter,
          kind: kindFilter,
        }),
        api.memories.sessions<MemorySession[]>(browsing.id),
      ]);
      memories = list || [];
      sessions = sess || [];
    } catch (e: any) {
      error = e.message || 'Failed to load memories';
    } finally {
      browseLoading = false;
    }
  }

  // Search as the operator types, debounced so a keystroke is not a request.
  function onQueryInput() {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => refreshBrowser(), 300);
  }

  function openCreateMemory() {
    editingMemory = null;
    memoryError = '';
    memoryForm = { text: '', kind: 'fact', session: sessionFilter, role: '', tags: '', importance: 0.5, expires_at: '' };
    showMemoryModal = true;
  }

  function openEditMemory(memory: Memory) {
    editingMemory = memory;
    memoryError = '';
    memoryForm = {
      text: memory.text,
      kind: memory.kind,
      session: memory.session || '',
      role: memory.role || '',
      tags: (memory.tags || []).join(', '),
      importance: memory.importance,
      expires_at: memory.expires_at ? memory.expires_at.slice(0, 16) : '',
    };
    showMemoryModal = true;
  }

  async function saveMemory() {
    if (!browsing) return;
    memoryError = '';
    if (!memoryForm.text.trim()) {
      memoryError = 'Text is required';
      return;
    }
    savingMemory = true;
    try {
      const body: Record<string, unknown> = {
        text: memoryForm.text.trim(),
        kind: memoryForm.kind,
        session: memoryForm.session.trim(),
        role: memoryForm.kind === 'turn' ? memoryForm.role : '',
        tags: memoryForm.tags.split(',').map((t) => t.trim()).filter(Boolean),
        importance: Number(memoryForm.importance) || 0,
      };
      if (memoryForm.expires_at) {
        body.expires_at = new Date(memoryForm.expires_at).toISOString();
      }
      if (editingMemory) await api.memories.updateMemory(browsing.id, editingMemory.id, body);
      else await api.memories.createMemory(browsing.id, body);
      showMemoryModal = false;
      await Promise.all([refreshBrowser(), memoryStoresStore.refresh()]);
    } catch (e: any) {
      memoryError = e.message || 'Failed to save the memory';
    } finally {
      savingMemory = false;
    }
  }

  async function removeMemory(memory: Memory) {
    if (!browsing) return;
    const ok = await confirm('Forget this memory?');
    if (!ok) return;
    try {
      await api.memories.removeMemory(browsing.id, memory.id);
      await Promise.all([refreshBrowser(), memoryStoresStore.refresh()]);
    } catch (e: any) {
      error = e.message || 'Failed to delete the memory';
    }
  }

  async function clearSession(session: MemorySession) {
    if (!browsing) return;
    const name = session.session || '(shared)';
    const ok = await confirm(
      `Clear session "${name}"? Its transcript and session-scoped facts are forgotten.`,
    );
    if (!ok) return;
    try {
      await api.memories.clearSession(browsing.id, session.session);
      await Promise.all([refreshBrowser(), memoryStoresStore.refresh()]);
    } catch (e: any) {
      error = e.message || 'Failed to clear the session';
    }
  }

  function connectionName(id: string): string {
    return $connectionsStore.connections.find((c) => c.id === id)?.name || id;
  }

  function shortDate(iso: string): string {
    return iso ? new Date(iso).toLocaleString() : '';
  }
</script>

<h2 class="text-xl font-bold mb-2">Memory</h2>
<p class="text-sm mb-6" style="color: var(--theme-text-muted, #a6adc8)">
  Named banks of memories the AI nodes use: conversation transcripts an agent
  resumes and facts it recalls. An AI Memory node names a store here, and any
  number of flows naming the same store share one memory. A store with an
  embedding connection recalls semantically; without one it recalls by
  keywords.
</p>

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

{#if canWrite}
  <div class="flex justify-end mb-4">
    <button class="btn btn-primary" onclick={openCreateStore}>
      <i class="las la-plus mr-1"></i>Add Memory Store
    </button>
  </div>
{/if}

{#if loading}
  <p class="opacity-60">Loading memory stores...</p>
{:else if stores.length === 0}
  <p class="opacity-60 text-center py-8">
    No memory stores yet. Add one, then wire an AI Memory node at it.
  </p>
{:else}
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
          <th class="py-2 pr-4 text-left">Name</th>
          <th class="py-2 pr-4 text-left">Recall</th>
          <th class="py-2 pr-4 text-left">Contents</th>
          <th class="py-2 text-right">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each stores as store (store.id)}
          <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
            <td class="py-2 pr-4">
              <button class="font-medium text-left hover:underline bg-transparent border-none cursor-pointer p-0"
                      style="color: var(--theme-text, #cdd6f4)"
                      onclick={() => openBrowser(store)}>
                {store.name}
              </button>
              {#if store.description}
                <div class="text-xs opacity-60">{store.description}</div>
              {/if}
            </td>
            <td class="py-2 pr-4 text-xs opacity-70">
              {#if store.embed_connection}
                {connectionName(store.embed_connection)} / {store.embed_model}
                {#if store.pending > 0}
                  <span title="Memories awaiting an embedding"> · {store.pending} pending</span>
                {/if}
              {:else}
                keyword only
              {/if}
            </td>
            <td class="py-2 pr-4 text-xs opacity-70">
              {store.facts} facts · {store.turns} turns · {store.sessions} sessions
            </td>
            <td class="py-2 text-right whitespace-nowrap">
              <button class="icon-btn" title="Browse memories" onclick={() => openBrowser(store)}>
                <i class="la la-search"></i>
              </button>
              {#if canWrite}
                <button class="icon-btn" title="Edit" onclick={() => openEditStore(store)}>
                  <i class="la la-pen"></i>
                </button>
                <button class="icon-btn icon-btn-danger" title="Delete" onclick={() => removeStore(store)}>
                  <i class="la la-trash"></i>
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

{#if browsing}
  <div class="mt-8 pt-4 border-t" style="border-color: var(--theme-surface1, #45475a)">
    <div class="flex items-center justify-between mb-3">
      <h3 class="text-lg font-semibold">
        {browsing.name}
        <span class="text-xs font-normal opacity-60 ml-2">memories</span>
      </h3>
      <div class="flex gap-2">
        {#if canWrite}
          <button class="btn btn-primary btn-sm" onclick={openCreateMemory}>
            <i class="las la-plus mr-1"></i>Add Memory
          </button>
        {/if}
        <button class="btn btn-secondary btn-sm" onclick={() => (browsing = null)}>Close</button>
      </div>
    </div>

    <div class="flex flex-wrap gap-2 mb-3">
      <input
        class="input flex-1 min-w-48"
        placeholder="Search the way agent recall does…"
        bind:value={query}
        oninput={onQueryInput}
      />
      <select class="input w-auto" bind:value={kindFilter} onchange={refreshBrowser}>
        <option value="">All kinds</option>
        <option value="fact">Facts</option>
        <option value="turn">Conversation turns</option>
      </select>
      <select class="input w-auto" bind:value={sessionFilter} onchange={refreshBrowser}>
        <option value="">All sessions</option>
        {#each sessions as s (s.session)}
          <option value={s.session}>{s.session || '(shared)'} · {s.turns} turns</option>
        {/each}
      </select>
      {#if canWrite && sessionFilter !== ''}
        {@const selected = sessions.find((s) => s.session === sessionFilter)}
        {#if selected}
          <button class="btn btn-secondary btn-sm" onclick={() => clearSession(selected)}>
            <i class="las la-broom mr-1"></i>Clear session
          </button>
        {/if}
      {/if}
    </div>

    {#if browseLoading}
      <p class="opacity-60">Loading…</p>
    {:else if memories.length === 0}
      <p class="opacity-60 text-center py-6">
        {query.trim() ? 'Nothing recalled for that query.' : 'Nothing remembered yet.'}
      </p>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b" style="border-color: var(--theme-surface1, #45475a)">
              {#if query.trim()}<th class="py-2 pr-3 text-left">Score</th>{/if}
              <th class="py-2 pr-3 text-left">Memory</th>
              <th class="py-2 pr-3 text-left">Kind</th>
              <th class="py-2 pr-3 text-left">Session</th>
              <th class="py-2 pr-3 text-left">Importance</th>
              <th class="py-2 pr-3 text-left">Created</th>
              {#if canWrite}<th class="py-2 text-right">Actions</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each memories as memory (memory.id)}
              <tr class="border-b align-top" style="border-color: var(--theme-surface1, #45475a)">
                {#if query.trim()}
                  <td class="py-2 pr-3 text-xs opacity-70">{(memory.score ?? 0).toFixed(2)}</td>
                {/if}
                <td class="py-2 pr-3">
                  <div class="memory-text">{memory.text}</div>
                  {#if (memory.tags || []).length > 0}
                    <div class="text-xs opacity-60 mt-0.5">
                      {#each memory.tags || [] as tag}
                        <span class="tag">{tag}</span>
                      {/each}
                    </div>
                  {/if}
                </td>
                <td class="py-2 pr-3 text-xs opacity-70">
                  {memory.kind}{memory.role ? ` · ${memory.role}` : ''}
                  {#if browsing.embed_connection && !memory.embedded}
                    <span title="Awaiting an embedding; recalled by keywords until it lands"> ⏳</span>
                  {/if}
                </td>
                <td class="py-2 pr-3 text-xs opacity-70">{memory.session || '—'}</td>
                <td class="py-2 pr-3 text-xs opacity-70">{memory.importance.toFixed(1)}</td>
                <td class="py-2 pr-3 text-xs opacity-70" title={memory.expires_at ? `Expires ${shortDate(memory.expires_at)}` : ''}>
                  {shortDate(memory.created_at)}{memory.expires_at ? ' ⌛' : ''}
                </td>
                {#if canWrite}
                  <td class="py-2 text-right whitespace-nowrap">
                    <button class="icon-btn" title="Edit" onclick={() => openEditMemory(memory)}>
                      <i class="la la-pen"></i>
                    </button>
                    <button class="icon-btn icon-btn-danger" title="Forget" onclick={() => removeMemory(memory)}>
                      <i class="la la-trash"></i>
                    </button>
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
{/if}

{#if showStoreModal}
  <Modal title={editingStore ? 'Edit Memory Store' : 'New Memory Store'} onclose={() => (showStoreModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveStore(); }} class="flex flex-col gap-3">
      {#if storeError}
        <div class="p-2 rounded text-sm"
             style="background-color: var(--theme-error); color: var(--theme-base);">
          {storeError}
        </div>
      {/if}
      <FormField label="Name" id="memstore-name">
        <input id="memstore-name" class="input w-full" bind:value={storeForm.name}
               placeholder="e.g. House" required />
      </FormField>
      <FormField label="Description" id="memstore-desc">
        <input id="memstore-desc" class="input w-full" bind:value={storeForm.description}
               placeholder="What this store remembers" />
      </FormField>
      <FormField label="Embedding connection" id="memstore-conn">
        <select id="memstore-conn" class="input w-full" bind:value={storeForm.embed_connection}
                onchange={() => loadEmbedModels(storeForm.embed_connection)}>
          <option value="">None — keyword recall only</option>
          {#each embedConnections as conn (conn.id)}
            <option value={conn.id}>{conn.name}</option>
          {/each}
        </select>
        <p class="text-xs mt-1" style="color: var(--theme-text-muted, #a6adc8)">
          Semantic recall through an AI provider's embeddings. Existing memories
          are re-embedded in the background when this changes.
        </p>
      </FormField>
      {#if storeForm.embed_connection}
        <FormField label="Embedding model" id="memstore-model">
          <input id="memstore-model" class="input w-full" bind:value={storeForm.embed_model}
                 placeholder="e.g. text-embedding-3-small" list="memstore-model-list" />
          <datalist id="memstore-model-list">
            {#each embedModels as model (model)}
              <option value={model}></option>
            {/each}
          </datalist>
        </FormField>
      {/if}
      <div class="grid grid-cols-2 gap-3">
        <FormField label="Recency half-life (days)" id="memstore-halflife">
          <input id="memstore-halflife" class="input w-full" type="number" min="1" step="1"
                 bind:value={storeForm.half_life_days} />
        </FormField>
        <FormField label="Recency weight (0–1)" id="memstore-recency">
          <input id="memstore-recency" class="input w-full" type="number" min="0" max="1" step="0.05"
                 bind:value={storeForm.recency_weight} />
        </FormField>
        <FormField label="Turns kept per session" id="memstore-turns">
          <input id="memstore-turns" class="input w-full" type="number" min="0" step="1"
                 bind:value={storeForm.max_session_turns} />
        </FormField>
        <FormField label="Fact cap (0 = unlimited)" id="memstore-cap">
          <input id="memstore-cap" class="input w-full" type="number" min="0" step="1"
                 bind:value={storeForm.max_memories} />
        </FormField>
      </div>
      <div class="flex gap-2 justify-end">
        <button type="button" class="btn btn-secondary" onclick={() => (showStoreModal = false)}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={savingStore}>
          {savingStore ? 'Saving...' : editingStore ? 'Save' : 'Create'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

{#if showMemoryModal && browsing}
  <Modal title={editingMemory ? 'Edit Memory' : 'New Memory'} onclose={() => (showMemoryModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveMemory(); }} class="flex flex-col gap-3">
      {#if memoryError}
        <div class="p-2 rounded text-sm"
             style="background-color: var(--theme-error); color: var(--theme-base);">
          {memoryError}
        </div>
      {/if}
      <FormField label="Text" id="memory-text">
        <textarea id="memory-text" class="input w-full" rows="3" bind:value={memoryForm.text}
                  placeholder="The fact to remember, phrased to stand alone" required></textarea>
      </FormField>
      <div class="grid grid-cols-2 gap-3">
        <FormField label="Kind" id="memory-kind">
          <select id="memory-kind" class="input w-full" bind:value={memoryForm.kind}>
            <option value="fact">Fact</option>
            <option value="turn">Conversation turn</option>
          </select>
        </FormField>
        {#if memoryForm.kind === 'turn'}
          <FormField label="Role" id="memory-role">
            <select id="memory-role" class="input w-full" bind:value={memoryForm.role}>
              <option value="user">User</option>
              <option value="assistant">Assistant</option>
            </select>
          </FormField>
        {:else}
          <FormField label="Importance (0–1)" id="memory-importance">
            <input id="memory-importance" class="input w-full" type="number" min="0" max="1" step="0.1"
                   bind:value={memoryForm.importance} />
          </FormField>
        {/if}
        <FormField label="Session" id="memory-session">
          <input id="memory-session" class="input w-full" bind:value={memoryForm.session}
                 placeholder="Empty = global to the store" />
        </FormField>
        <FormField label="Expires" id="memory-expires">
          <input id="memory-expires" class="input w-full" type="datetime-local"
                 bind:value={memoryForm.expires_at} />
        </FormField>
      </div>
      <FormField label="Tags" id="memory-tags">
        <input id="memory-tags" class="input w-full" bind:value={memoryForm.tags}
               placeholder="comma, separated" />
      </FormField>
      <div class="flex gap-2 justify-end">
        <button type="button" class="btn btn-secondary" onclick={() => (showMemoryModal = false)}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={savingMemory}>
          {savingMemory ? 'Saving...' : editingMemory ? 'Save' : 'Store'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  .memory-text {
    max-width: 32rem;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .tag {
    display: inline-block;
    padding: 0 0.4rem;
    margin-right: 0.25rem;
    border-radius: 3px;
    background-color: var(--theme-surface1, #45475a);
  }

  .btn-sm {
    padding: 0.25rem 0.6rem;
    font-size: 0.8rem;
  }

  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    border: none;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    transition: background-color 0.15s ease;
  }

  .icon-btn:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .icon-btn-danger:hover {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }
</style>
