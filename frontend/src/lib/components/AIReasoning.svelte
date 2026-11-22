<script lang="ts">
  // AIReasoning shows a model's disclosed reasoning above its reply. It starts
  // collapsed because reasoning often runs far longer than the answer.
  let { text }: { text: string } = $props();
</script>

<details class="reasoning">
  <summary><i class="las la-brain mr-1" aria-hidden="true"></i>Reasoning</summary>
  <div class="body">{text}</div>
</details>

<style>
  .reasoning { font-size: .8rem; margin-bottom: .4rem; }
  .reasoning summary { cursor: pointer; opacity: .7; width: fit-content; }
  .reasoning summary:hover { opacity: 1; }
  .body {
    white-space: pre-wrap; overflow-wrap: anywhere; max-height: 240px; overflow-y: auto;
    margin-top: .35rem; padding-left: .6rem; opacity: .8;
    border-left: 2px solid var(--theme-surface1);
  }
</style>
