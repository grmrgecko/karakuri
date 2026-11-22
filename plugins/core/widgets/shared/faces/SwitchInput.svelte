<script lang="ts">
  // A switch shows reported state, never the tap. Clicking a checkbox moves it
  // whether or not the device agreed, so it is put back where the source says
  // it is once the call settles, and a device that never reports keeps its last
  // known state rather than showing what someone wished for.
  let {
    checked = false,
    disabled = false,
    onchange,
  }: {
    checked?: boolean;
    disabled?: boolean;
    onchange?: (next: boolean) => void | Promise<void>;
  } = $props();

  async function handle(e: Event & { currentTarget: HTMLInputElement }): Promise<void> {
    const box = e.currentTarget;
    await onchange?.(box.checked);
    box.checked = checked;
  }
</script>

<label class="switch">
  <input type="checkbox" {checked} {disabled} onchange={handle} />
  <span class="track"></span>
</label>

<style>
  .switch {
    position: relative;
    display: inline-block;
    width: 34px;
    height: 20px;
    flex-shrink: 0;
  }

  .switch input {
    opacity: 0;
    width: 0;
    height: 0;
  }

  .track {
    position: absolute;
    cursor: pointer;
    inset: 0;
    background-color: var(--theme-surface2, #585b70);
    border-radius: 20px;
    transition: background-color 0.15s;
  }

  .track::before {
    content: "";
    position: absolute;
    height: 14px;
    width: 14px;
    left: 3px;
    bottom: 3px;
    background-color: var(--theme-base, #1e1e2e);
    border-radius: 50%;
    transition: transform 0.15s;
  }

  .switch input:checked + .track {
    background-color: var(--theme-accent, #89b4fa);
  }

  .switch input:checked + .track::before {
    transform: translateX(14px);
  }

  .switch input:disabled + .track {
    cursor: default;
    opacity: 0.5;
  }
</style>
