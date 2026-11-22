<script lang="ts">
  import { highlight, previewLines, languageLabel } from '../../utils/highlight';
  import type { GuideRef } from '../../stores/guides';
  import type { ScriptTest } from './scriptTest';

  let {
    id,
    title,
    language,
    value,
    emptyText = 'Nothing here yet — click to write something.',
    saveLabel = 'Save',
    hint = 'Ctrl+Enter saves',
    previewLineCount = 6,
    docs = null,
    test = null,
    scriptAnalysis = false,
    validate,
    onsave,
  }: {
    id?: string;
    // title names the document in the editor's title bar; it is the field's
    // label, so an author knows which box they opened.
    title: string;
    language: string;
    value: string;
    emptyText?: string;
    saveLabel?: string;
    hint?: string;
    // previewLineCount caps the collapsed view without changing the editor.
    previewLineCount?: number;
    // docs names the reference page the editor's toolbar offers, or null for a
    // field with nothing to explain.
    docs?: GuideRef | null;
    // test offers the editor's Test button; see ./scriptTest.
    test?: ScriptTest | null;
    // scriptAnalysis enables server flow node functions for this document only.
    scriptAnalysis?: boolean;
    // validate rejects a document before it is stored, returning the reason.
    validate?: (next: string) => string | null;
    onsave: (next: string) => void;
  } = $props();

  // The editor pulls in CodeMirror, which is larger than the rest of the app
  // put together. It is loaded on first open so a flow whose nodes are never
  // opened for editing never pays for it. Once loaded it stays cached.
  let ScriptEditorComponent = $state<any>(null);
  let loadError = $state('');
  let open = $state(false);

  async function openEditor() {
    loadError = '';
    if (!ScriptEditorComponent) {
      try {
        ScriptEditorComponent = (await import('./ScriptEditor.svelte')).default;
      } catch (e: any) {
        loadError = e?.message || 'Failed to load the editor';
        return;
      }
    }
    open = true;
  }

  let lines = $derived(previewLines(value, previewLineCount));
  let total = $derived(value.split('\n').length);
</script>

<!--
  A multi-line config value is edited here rather than in a textarea: the panel
  is too narrow to write a template, a payload, or a script in, so the panel
  shows a syntax-coloured window onto the document and the full editor opens
  over the canvas when it is clicked.
-->
<button
  type="button"
  class="document-preview"
  {id}
  onclick={openEditor}
  title="Click to edit in a larger editor"
>
  <span class="document-bar">
    <span class="document-language">{languageLabel(language)}</span>
    <span class="document-edit"><i class="las la-expand"></i> Edit</span>
  </span>
  {#if value.trim() === ''}
    <span class="document-empty">{emptyText}</span>
  {:else}
    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
    <pre class="document-code">{@html highlight(lines.join('\n'), language)}</pre>
    {#if total > lines.length}
      <span class="document-more">+{total - lines.length} more lines</span>
    {/if}
  {/if}
</button>

{#if loadError}
  <p class="text-xs mt-1" style="color: var(--theme-error);">{loadError}</p>
{/if}

{#if open && ScriptEditorComponent}
  <ScriptEditorComponent
    {title}
    {language}
    {value}
    {saveLabel}
    {hint}
    {docs}
    {test}
    {scriptAnalysis}
    {validate}
    {onsave}
    onclose={() => (open = false)}
  />
{/if}

<style>
  .document-preview {
    display: block;
    width: 100%;
    padding: 0;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-crust, #11111b);
    text-align: left;
    cursor: pointer;
    overflow: hidden;
  }

  .document-preview:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .document-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 8px;
    border-bottom: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-mantle, #181825);
    font-size: 10px;
  }

  .document-language {
    font-weight: 600;
    opacity: 0.75;
  }

  .document-edit {
    opacity: 0.55;
  }

  .document-preview:hover .document-edit {
    opacity: 1;
    color: var(--theme-accent, #89b4fa);
  }

  .document-code {
    margin: 0;
    padding: 8px;
    max-height: 140px;
    overflow: hidden;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 11px;
    line-height: 1.5;
    white-space: pre;
    color: var(--theme-text, #cdd6f4);
  }

  .document-empty {
    display: block;
    padding: 12px 8px;
    font-size: 11px;
    opacity: 0.5;
  }

  .document-more {
    display: block;
    padding: 0 8px 6px;
    font-size: 10px;
    opacity: 0.45;
  }

  /* Token colors mirror the CodeMirror highlight style in scriptLang.ts so a
     document reads the same collapsed here as it does in the editor. */
  .document-code :global(.tok-comment) {
    color: var(--theme-overlay0, #6c7086);
    font-style: italic;
  }
  .document-code :global(.tok-string) {
    color: var(--theme-success, #a6e3a1);
  }
  .document-code :global(.tok-keyword) {
    color: var(--theme-error, #f38ba8);
  }
  .document-code :global(.tok-literal),
  .document-code :global(.tok-number) {
    color: var(--theme-warning, #f9e2af);
  }
  .document-code :global(.tok-builtin),
  .document-code :global(.tok-variable) {
    color: var(--theme-accent, #89b4fa);
  }
  .document-code :global(.tok-decorator),
  .document-code :global(.tok-property) {
    color: var(--theme-info, #94e2d5);
  }
</style>
