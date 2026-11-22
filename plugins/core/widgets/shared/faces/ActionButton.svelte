<script lang="ts">
  // One pressable action. The result of a press shows on the button itself for
  // a moment, because a dashboard has nowhere else to say a command landed.
  let {
    label = "",
    title = "",
    state = "",
    disabled = false,
    active = false,
    onpress,
  }: {
    label?: string;
    title?: string;
    state?: "" | "armed" | "ok" | "error";
    disabled?: boolean;
    active?: boolean;
    onpress?: () => void | Promise<void>;
  } = $props();
</script>

<button
  class="action"
  class:armed={state === "armed"}
  class:ok={state === "ok"}
  class:failed={state === "error"}
  class:active
  {disabled}
  title={title || label}
  onclick={() => onpress?.()}
>
  {state === "armed" ? "Confirm" : state === "ok" ? "Done" : label}
</button>

<style>
  .action {
    padding: 0.15rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    font-family: inherit;
    font-size: 0.8rem;
    line-height: 1.4;
    cursor: pointer;
    white-space: nowrap;
  }

  .action:hover:not(:disabled) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .action:disabled {
    cursor: default;
    opacity: 0.5;
  }

  /* The active state is the choice a select currently holds, drawn as a
     pressed key rather than a highlighted one so a segmented row reads as a
     row of keys. */
  .action.active {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    border-color: var(--theme-accent, #89b4fa);
  }

  .action.armed {
    color: var(--theme-warning, #f9e2af);
    border-color: var(--theme-warning, #f9e2af);
  }

  .action.ok {
    color: var(--theme-success, #a6e3a1);
    border-color: var(--theme-success, #a6e3a1);
  }

  .action.failed {
    color: var(--theme-error, #f38ba8);
    border-color: var(--theme-error, #f38ba8);
  }
</style>
