<script lang="ts">
  import { confirmDialog, resolveConfirm } from '../stores/confirmDialog';
  import Modal from './Modal.svelte';
</script>

{#if $confirmDialog.show}
  <Modal title="Confirm" zIndex={70} onclose={() => resolveConfirm(false)}>
    <p class="text-sm">{$confirmDialog.message}</p>
    <div class="flex justify-end gap-2">
      <button class="btn btn-secondary" onclick={() => resolveConfirm(false)}>Cancel</button>
      <button class="btn btn-danger" onclick={() => resolveConfirm(true)}>Confirm</button>
    </div>
  </Modal>
{/if}
