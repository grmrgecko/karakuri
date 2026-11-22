<script lang="ts">
  // Colour is a hex string everywhere in karakuri: it survives JSON, a device
  // script reads it without a library, and the browser has a picker for it. A
  // light that has never reported one shows white rather than black, which is
  // what an unlit bulb would otherwise look like.
  let {
    value = "",
    disabled = false,
    oncommit,
  }: {
    value?: string;
    disabled?: boolean;
    oncommit?: (next: string) => void | Promise<void>;
  } = $props();

  let shown = $derived(/^#[0-9a-fA-F]{6}$/.test(value || "") ? value : "#ffffff");
</script>

<input
  class="swatch"
  type="color"
  {disabled}
  value={shown}
  onchange={(e) => oncommit?.(e.currentTarget.value)}
/>

<style>
  .swatch {
    width: 1.8rem;
    height: 1.4rem;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
  }

  .swatch:disabled {
    cursor: default;
    opacity: 0.5;
  }
</style>
