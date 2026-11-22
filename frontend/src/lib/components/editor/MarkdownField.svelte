<script lang="ts">
  import { markdown } from '../../stores/markdown';
  import {
    promptAttachmentSet, attachmentSize, attachmentKey, readPromptAttachment,
    promptAttachmentReference, resolvePromptAssets, localPromptAssets,
    maxAttachments, maxAttachmentTotal, promptImageTypes, type PromptAttachment,
  } from '../../utils/promptAttachments';

  let { id, title, value, attachments, onsave }: {
    id: string; title: string; value: string; attachments: unknown;
    onsave: (text: string, files: Record<string, PromptAttachment>) => void;
  } = $props();

  let open = $state(false);
  let draft = $state('');
  let files = $state<Record<string, PromptAttachment>>({});
  let mode = $state<'write' | 'preview'>('write');
  let rendered = $state('');
  let error = $state('');
  let busy = $state(false);
  let picker = $state<HTMLInputElement | null>(null);
  let ScriptEditorComponent = $state<any>(null);
  let editor = $state<any>(null);
  let previewHTML = $derived(resolvePromptAssets(rendered, localPromptAssets(files)));

  async function edit() {
    draft = value; files = { ...promptAttachmentSet(attachments) };
    error = ''; mode = 'write';
    try {
      ScriptEditorComponent ??= (await import('./ScriptEditor.svelte')).default;
      open = true;
    } catch (e: any) { error = e.message || 'Could not load the editor.'; }
  }

  async function showPreview() {
    const source = editor?.getValue() ?? draft;
    try { rendered = await markdown.render(source); mode = 'preview'; error = ''; }
    catch (e: any) { error = e.message || 'Could not render Markdown.'; }
  }

  function insert(key: string, image = true) {
    const reference = promptAttachmentReference(key, files[key], image);
    const start = editor?.getSelection().from ?? draft.length;
    const prefix = start > 0 && draft[start - 1] !== '\n' ? '\n\n' : '';
    mode = 'write';
    editor?.replaceSelection(prefix + reference);
  }

  async function attach(selected: FileList | File[] | null | undefined) {
    if (!selected?.length || busy) return;
    busy = true; error = '';
    try {
      for (const file of Array.from(selected)) {
        if (Object.keys(files).length >= maxAttachments) throw new Error('At most 20 attachments are allowed.');
        const attachment = await readPromptAttachment(file);
        const total = Object.values(files).reduce((sum, a) => sum + attachmentSize(a), 0);
        if (total + attachmentSize(attachment) > maxAttachmentTotal) throw new Error('Attachments exceed 32 MiB in total.');
        const key = attachmentKey(file.name, Object.keys(files));
        files = { ...files, [key]: attachment };
        insert(key);
      }
    } catch (e: any) { error = e.message || 'Could not attach the file.'; }
    finally { busy = false; }
  }

  function remove(key: string) {
    const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    draft = draft.replace(new RegExp(`!?\\[(?:\\\\.|[^\\]])*\\]\\(attachment:${escaped}\\)`, 'g'), '');
    editor?.replaceDocument(draft);
    const next = { ...files }; delete next[key]; files = next;
    if (mode === 'preview') void showPreview();
  }
</script>

<button {id} type="button" class="input w-full text-left text-sm" onclick={edit}>
  <span class="block text-xs text-text-muted mb-2">Markdown · Edit instructions and attachments</span>
  <span class="block whitespace-pre-wrap max-h-28 overflow-hidden">{value || 'Click to write instructions.'}</span>
</button>

{#if error && !open}<p class="text-sm text-error" role="alert">{error}</p>{/if}

{#if open && ScriptEditorComponent}
  <ScriptEditorComponent
    bind:this={editor} {title} language="markdown" value={draft}
    saveLabel="Save instructions" hint="Ctrl+Enter saves"
    saveDisabled={busy} previewing={mode === 'preview'}
    onchange={(next: string) => (draft = next)}
    onfiles={(selected: File[]) => { void attach(selected); }}
    onsave={(text: string) => onsave(text, files)} onclose={() => (open = false)}
  >
    {#snippet toolbar()}
      <div class="flex gap-2 items-center">
        <button type="button" class="btn" class:btn-primary={mode === 'write'} onclick={() => (mode = 'write')}>Write</button>
        <button type="button" class="btn" class:btn-primary={mode === 'preview'} onclick={showPreview}>Preview</button>
        <button type="button" class="btn" disabled={busy} onclick={() => picker?.click()}>Attach image or file</button>
        <input bind:this={picker} type="file" multiple class="hidden" onchange={(e) => { void attach(e.currentTarget.files); e.currentTarget.value = ''; }} />
      </div>
    {/snippet}
    {#snippet preview()}
      <div class="prompt-markdown min-h-64 rounded border border-surface1 p-4">{@html previewHTML}</div>
      <p class="text-xs text-text-muted">Expressions are resolved against the incoming message when the node runs.</p>
    {/snippet}
    {#snippet children()}
      <p class="text-xs text-text-muted">Up to 20 attachments, 8 MiB each, 32 MiB total. Images embed; other files download.</p>
      {#each Object.entries(files) as [key, file] (key)}
        <div class="flex gap-2 items-center text-sm">
          <span class="flex-1 min-w-0 truncate" title={file.filename}>{file.filename}</span>
          {#if promptImageTypes.includes(file.type)}<button type="button" class="btn text-xs" onclick={() => insert(key)}>Embed</button>{/if}
          <button type="button" class="btn text-xs" onclick={() => insert(key, false)}>Link</button>
          <button type="button" class="btn text-xs" onclick={() => remove(key)}>Remove</button>
        </div>
      {/each}
      {#if error}<p class="text-sm text-error" role="alert">{error}</p>{/if}
    {/snippet}
  </ScriptEditorComponent>
{/if}
