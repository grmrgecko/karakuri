<script lang="ts">
  interface ButtonChoice { id: string; label: string }
  let { value, onChange }: { value: unknown; onChange: (buttons: ButtonChoice[]) => void } = $props();
  let buttons = $derived((Array.isArray(value) ? value : [
    { id: 'retry', label: 'Retry' }, { id: 'bypass', label: 'Bypass' }, { id: 'stop', label: 'Stop' },
  ]) as ButtonChoice[]);

  function update(index: number, patch: Partial<ButtonChoice>) {
    onChange(buttons.map((button, i) => i === index ? { ...button, ...patch } : button));
  }
  function add() {
    const id = `choice-${crypto.randomUUID().slice(0, 8)}`;
    onChange([...buttons, { id, label: 'Continue' }]);
  }
  function move(index: number, direction: number) {
    const next = [...buttons];
    [next[index], next[index + direction]] = [next[index + direction], next[index]];
    onChange(next);
  }
</script>

<div class="flex flex-col gap-2">
  {#each buttons as button, i (i)}
    <div class="border border-surface1 rounded p-2">
      <div class="flex items-center gap-1 mb-2">
        <input class="input w-full min-w-0 text-sm" aria-label={`Button ${i + 1} label`} value={button.label} placeholder="Button label" oninput={(e) => update(i, { label: e.currentTarget.value })} />
        <button type="button" class="btn p-1" title="Move up" disabled={i === 0} onclick={() => move(i, -1)}><i class="las la-arrow-up"></i></button>
        <button type="button" class="btn p-1" title="Move down" disabled={i === buttons.length - 1} onclick={() => move(i, 1)}><i class="las la-arrow-down"></i></button>
        <button type="button" class="btn p-1" aria-label={`Remove button ${i + 1}`} onclick={() => onChange(buttons.filter((_, index) => index !== i))}><i class="las la-times"></i></button>
      </div>
      <label class="block text-xs text-text-muted">Output ID
        <input class="input w-full text-xs mt-1" value={button.id} placeholder="choice-id" onchange={(e) => update(i, { id: e.currentTarget.value.trim() })} />
      </label>
    </div>
  {/each}
  <button type="button" class="btn text-sm" disabled={buttons.length >= 12} onclick={add}>Add button</button>
</div>
