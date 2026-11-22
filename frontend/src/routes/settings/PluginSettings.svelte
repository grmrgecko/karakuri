<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from 'svelte-routing';
  import { pluginStore } from '../../lib/stores/plugins';

  let { name }: { name: string } = $props();

  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let success = $state('');
  let schema: any = $state(null);
  let values: Record<string, string> = $state({});

  onMount(async () => {
    try {
      const data = await pluginStore.getSettings(name);
      values = data.values || {};
      if (data.schema) {
        schema = JSON.parse(data.schema);
      }
    } catch (e: any) {
      error = e.message || 'Failed to load plugin settings';
    }
    loading = false;
  });

  // The form is driven entirely by the plugin's JSON Schema, so a plugin adds
  // a setting without any frontend change.
  let properties = $derived(
    schema?.properties
      ? Object.entries(schema.properties).map(([key, prop]) => ({ key, prop: prop as any }))
      : []
  );

  // A schema's "password" format is what keeps an API key out of the DOM in
  // plain text; everything else falls back to its JSON type.
  function inputType(prop: any): string {
    if (prop.format === 'password') return 'password';
    if (prop.type === 'integer' || prop.type === 'number') return 'number';
    return 'text';
  }

  function currentValue(key: string, prop: any): string {
    if (key in values) return values[key];
    return prop.default != null ? String(prop.default) : '';
  }

  function currentBool(key: string, prop: any): boolean {
    if (key in values) return values[key] === 'true';
    return !!prop.default;
  }

  function setValue(key: string, event: Event) {
    values[key] = (event.target as HTMLInputElement | HTMLSelectElement).value;
  }

  function setBool(key: string, event: Event) {
    values[key] = (event.target as HTMLInputElement).checked ? 'true' : 'false';
  }

  async function save() {
    saving = true;
    error = '';
    success = '';
    try {
      // Unset fields still carry their schema default, so a save writes the
      // whole set rather than leaving gaps the plugin has to guess at.
      const payload: Record<string, string> = {};
      for (const { key, prop } of properties) {
        payload[key] = prop.type === 'boolean'
          ? String(currentBool(key, prop))
          : currentValue(key, prop);
      }
      await pluginStore.updateSettings(name, payload);
      success = 'Settings saved. The plugin reloaded to pick them up.';
    } catch (e: any) {
      error = e.message || 'Failed to save settings';
    }
    saving = false;
  }

  function back() {
    navigate('/settings/plugins');
  }
</script>

<div class="plugin-settings">
  <button class="back-link" onclick={back}>
    <i class="las la-arrow-left"></i> Plugins
  </button>

  <h2 class="text-xl font-bold mb-6">{name} settings</h2>

  {#if loading}
    <p class="opacity-60">Loading settings...</p>
  {:else if properties.length === 0}
    <p class="opacity-60">This plugin has no configurable settings.</p>
    {#if error}
      <p class="message error mt-2">{error}</p>
    {/if}
  {:else}
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); save(); }} class="settings-form">
      {#each properties as { key, prop } (key)}
        <div class="form-field">
          <label for="setting-{key}">{prop.title || key}</label>
          {#if prop.description}
            <p class="field-description">{prop.description}</p>
          {/if}

          {#if prop.enum}
            <select id="setting-{key}" value={currentValue(key, prop)}
                    onchange={(e: Event) => setValue(key, e)}>
              {#each prop.enum as option}
                <option value={String(option)}>{option}</option>
              {/each}
            </select>
          {:else if prop.type === 'boolean'}
            <label class="checkbox-label">
              <input type="checkbox" id="setting-{key}"
                     checked={currentBool(key, prop)}
                     onchange={(e: Event) => setBool(key, e)} />
              <span>Enabled</span>
            </label>
          {:else}
            <input type={inputType(prop)} id="setting-{key}"
                   value={currentValue(key, prop)}
                   placeholder={prop.default != null ? String(prop.default) : ''}
                   oninput={(e: Event) => setValue(key, e)} />
          {/if}
        </div>
      {/each}

      {#if error}
        <p class="message error">{error}</p>
      {/if}
      {#if success}
        <p class="message success">{success}</p>
      {/if}

      <div class="form-actions">
        <button type="button" class="btn" onclick={back}>Cancel</button>
        <button type="submit" class="btn btn-primary" disabled={saving}>
          {saving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </form>
  {/if}
</div>

<style>
  .plugin-settings {
    max-width: 600px;
  }

  .back-link {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    background: none;
    border: none;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.875rem;
    cursor: pointer;
    padding: 0;
    margin-bottom: 1rem;
  }

  .back-link:hover {
    text-decoration: underline;
  }

  .settings-form {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }

  .form-field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  .form-field label {
    font-size: 0.875rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
  }

  .field-description {
    font-size: 0.75rem;
    color: var(--theme-subtext0, #a6adc8);
    margin: 0;
  }

  .form-field input[type="text"],
  .form-field input[type="password"],
  .form-field input[type="number"],
  .form-field select {
    padding: 0.5rem 0.75rem;
    border-radius: 4px;
    border: 1px solid var(--theme-border, #45475a);
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.875rem;
  }

  .form-field input:focus,
  .form-field select:focus {
    outline: none;
    border-color: var(--theme-accent, #89b4fa);
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 400 !important;
    cursor: pointer;
  }

  .checkbox-label input[type="checkbox"] {
    width: 1rem;
    height: 1rem;
    accent-color: var(--theme-accent, #89b4fa);
  }

  .form-actions {
    display: flex;
    gap: 0.5rem;
    justify-content: flex-end;
    margin-top: 0.5rem;
  }

  .message {
    font-size: 0.8rem;
    margin: 0;
  }

  .message.error {
    color: var(--theme-red, #f38ba8);
  }

  .message.success {
    color: var(--theme-green, #a6e3a1);
  }
</style>
