<script lang="ts">
  import { onMount } from 'svelte';
  import { auth, currentUser, hasPermission, preferences } from '../../lib/stores/auth';
  import { dashboards } from '../../lib/stores/dashboards';
  import { OVERVIEW } from '../../lib/homeStart';
  import { usersStore } from '../../lib/stores/users';
  import FormField from '../../lib/components/FormField.svelte';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  let saving = $state(false);
  let message = $state('');
  let error = $state('');

  let email = $state('');
  let oldPassword = $state('');
  let newPassword = $state('');
  let confirmPassword = $state('');

  let user = $derived($currentUser);
  $effect(() => {
    if (user) {
      email = user.email;
    }
  });

  // The start page lists only dashboards the server says this user can see;
  // a stored choice that is no longer among them is shown without its name.
  let startChoices: { id: string; name: string }[] = $state([]);
  let startPage = $derived(typeof $preferences.start_page === 'string' ? $preferences.start_page : '');
  let startUnavailable = $derived(
    startPage !== '' && startPage !== OVERVIEW && !startChoices.some((d) => d.id === startPage),
  );

  onMount(async () => {
    if (!hasPermission('api:dashboards:read')) return;
    try {
      startChoices = (await dashboards.listAll()).map((d) => ({ id: d.id, name: d.name }));
    } catch {
      startChoices = [];
    }
  });

  function setStartPage(value: string) {
    auth.setPreferences({ start_page: value === '' ? null : value });
  }

  async function updateProfile() {
    error = '';
    message = '';
    saving = true;
    try {
      if (!user) return;
      await usersStore.update(user.id, { email });
      message = 'Profile updated successfully.';
      await auth.init();
    } catch (e: any) {
      error = e.message || 'Failed to update profile';
    } finally {
      saving = false;
    }
  }

  async function changePassword() {
    error = '';
    message = '';

    if (newPassword !== confirmPassword) {
      error = 'Passwords do not match.';
      return;
    }
    if (newPassword.length < 8) {
      error = 'Password must be at least 8 characters.';
      return;
    }

    saving = true;
    try {
      if (!user) return;
      await usersStore.updatePassword(user.id, newPassword, oldPassword);
      message = 'Password changed successfully.';
      oldPassword = '';
      newPassword = '';
      confirmPassword = '';
    } catch (e: any) {
      error = e.message || 'Failed to change password';
    } finally {
      saving = false;
    }
  }
</script>

{#if message}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-success, #a6e3a1); color: var(--theme-base);">
    {message}
  </div>
{/if}

{#if error}
  <div class="mb-4 p-3 rounded text-sm"
       style="background-color: var(--theme-error); color: var(--theme-base);">
    {error}
  </div>
{/if}

<PageHeader title="Profile" />

<form onsubmit={(e: SubmitEvent) => { e.preventDefault(); updateProfile(); }} class="card p-6 flex flex-col gap-4 mb-6">
  <div>
    <span class="block text-sm font-medium mb-1">Username</span>
    <div class="text-sm px-3 py-2 rounded-md" style="background-color: var(--theme-surface0); opacity: 0.7;">
      {user?.username ?? ''}
    </div>
  </div>
  <FormField label="Email" id="prof-email">
    <input id="prof-email" type="email" class="input w-full" bind:value={email} required />
  </FormField>
  <button type="submit" class="btn btn-primary self-start" disabled={saving}>
    <i class="las la-save mr-1"></i>{saving ? 'Saving...' : 'Save'}
  </button>
</form>

<PageHeader title="Start page" level="section" />
<div class="card p-6 flex flex-col gap-2 mb-6">
  <FormField label="Open at sign-in and from the Karakuri name" id="prof-start">
    <select id="prof-start" class="input w-full" value={startPage} onchange={(e) => setStartPage(e.currentTarget.value)}>
      <option value="">Default</option>
      <option value={OVERVIEW}>Overview</option>
      {#each startChoices as d (d.id)}
        <option value={d.id}>Dashboard: {d.name}</option>
      {/each}
      {#if startUnavailable}
        <option value={startPage}>Unavailable dashboard</option>
      {/if}
    </select>
  </FormField>
  <p class="text-xs opacity-60">
    {#if startUnavailable}
      The chosen dashboard was deleted or is no longer shared with you, so the default opens instead.
    {:else}
      The default is the overview if you can read flows, otherwise your dashboards.
    {/if}
  </p>
</div>

<PageHeader title="Change Password" level="section" />
<form onsubmit={(e: SubmitEvent) => { e.preventDefault(); changePassword(); }} class="card p-6 flex flex-col gap-4">
  <FormField label="Current Password" id="pw-old">
    <input id="pw-old" type="password" class="input w-full" bind:value={oldPassword} required />
  </FormField>
  <FormField label="New Password" id="pw-new">
    <input id="pw-new" type="password" class="input w-full" bind:value={newPassword} required />
  </FormField>
  <FormField label="Confirm New Password" id="pw-confirm">
    <input id="pw-confirm" type="password" class="input w-full" bind:value={confirmPassword} required />
  </FormField>
  <button type="submit" class="btn btn-primary self-start" disabled={saving}>
    <i class="las la-key mr-1"></i>{saving ? 'Changing...' : 'Change Password'}
  </button>
</form>
