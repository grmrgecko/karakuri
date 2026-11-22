<script lang="ts">
  import { Router, Route, link, useLocation, navigate } from 'svelte-routing';
  import { isAuthenticated, hasPermission } from '../../lib/stores/auth';
  import Overview from './Overview.svelte';
  import Profile from './Profile.svelte';
  import Sessions from './Sessions.svelte';
  import Users from './Users.svelte';
  import Groups from './Groups.svelte';
  import Roles from './Roles.svelte';
  import ApiKeys from './ApiKeys.svelte';
  import Connections from './Connections.svelte';
  import Notifications from './Notifications.svelte';
  import Controls from './Controls.svelte';
  import Memories from './Memories.svelte';
  import MQTTDevices from './MQTTDevices.svelte';
  import History from './History.svelte';
  import Energy from './Energy.svelte';
  import Backup from './Backup.svelte';
  import EventLog from './EventLog.svelte';
  import Themes from './Themes.svelte';
  import Plugins from './Plugins.svelte';
  import Agents from './Agents.svelte';
  import PluginSettings from './PluginSettings.svelte';
  import ResourceShare from './ResourceShare.svelte';

  const location = useLocation();

  // Every settings page the router serves. A path outside this set is a stale
  // bookmark — a panel that has since been removed, say — and lands on the
  // overview rather than on a blank pane.
  const SECTIONS = [
    'profile', 'api-keys', 'sessions', 'themes',
    'users', 'groups', 'roles', 'connections', 'notifications', 'controls', 'memories', 'mqtt-devices', 'energy', 'history', 'plugins', 'agents', 'backup', 'event-log',
  ];

  $effect(() => {
    if (!$isAuthenticated) {
      navigate('/login', { replace: true });
    }
  });

  $effect(() => {
    const rest = $location.pathname.replace(/^\/settings\/?/, '').replace(/\/$/, '');
    if (rest !== '' && !SECTIONS.includes(rest.split('/')[0])) {
      navigate('/settings', { replace: true });
    }
  });

  let canViewUsers = $derived(hasPermission('api:users:read'));
  let canViewGroups = $derived(hasPermission('api:groups:read'));
  let canViewRoles = $derived(hasPermission('api:roles:read'));
  let canViewPlugins = $derived(hasPermission('api:plugins:read'));
  let canViewAgents = $derived(hasPermission('api:agents:read'));
  let canViewControls = $derived(hasPermission('api:controls:read'));
  let canViewMemories = $derived(hasPermission('api:memories:read'));
  let canViewConnections = $derived(hasPermission('api:connections:read'));
  let canViewBackups = $derived(hasPermission('api:backups:read'));
  let canEditEventLog = $derived(hasPermission('api:events:write'));
  let pathname = $derived($location.pathname);

  let navItems = $derived([
    { href: '/settings', icon: 'la-tachometer-alt', label: 'Overview' },
    { href: '/settings/profile', icon: 'la-user', label: 'Profile' },
    { href: '/settings/api-keys', icon: 'la-key', label: 'API Keys' },
    { href: '/settings/sessions', icon: 'la-desktop', label: 'Sessions' },
    { href: '/settings/themes', icon: 'la-palette', label: 'Themes' },
    ...(canViewUsers ? [{ href: '/settings/users', icon: 'la-users', label: 'Users' }] : []),
    ...(canViewGroups ? [{ href: '/settings/groups', icon: 'la-layer-group', label: 'Groups' }] : []),
    ...(canViewRoles ? [{ href: '/settings/roles', icon: 'la-shield-alt', label: 'Roles' }] : []),
    { href: '/settings/connections', icon: 'la-plug', label: 'Connections' },
    ...(canViewConnections ? [{ href: '/settings/notifications', icon: 'la-bell', label: 'Notifications' }] : []),
    ...(canViewControls ? [{ href: '/settings/controls', icon: 'la-sliders-h', label: 'Controls' }] : []),
    ...(canViewMemories ? [{ href: '/settings/memories', icon: 'la-book', label: 'Memory' }] : []),
    ...(canViewConnections
      ? [{ href: '/settings/mqtt-devices', icon: 'la-microchip', label: 'Devices' }]
      : []),
    ...(canViewConnections
      ? [{ href: '/settings/energy', icon: 'la-bolt', label: 'Energy' }]
      : []),
    ...(canViewConnections
      ? [{ href: '/settings/history', icon: 'la-history', label: 'History forwarding' }]
      : []),
    ...(canViewPlugins ? [{ href: '/settings/plugins', icon: 'la-puzzle-piece', label: 'Plugins' }] : []),
    ...(canViewAgents ? [{ href: '/settings/agents', icon: 'la-server', label: 'Agents' }] : []),
    ...(canViewBackups ? [{ href: '/settings/backup', icon: 'la-archive', label: 'Backup' }] : []),
    ...(canEditEventLog ? [{ href: '/settings/event-log', icon: 'la-stream', label: 'Event log' }] : []),
  ].map(item => ({
    ...item,
    active: item.href === '/settings'
      ? (pathname === '/settings' || pathname === '/settings/')
      : pathname.startsWith(item.href),
  })));
</script>

<div class="settings-layout">
  <aside class="sidebar">
    <nav class="sidebar-nav">
      {#each navItems as item (item.href)}
        <a href={item.href} use:link class="sidebar-item" class:active={item.active}>
          <i class="las {item.icon}"></i>
          <span>{item.label}</span>
        </a>
      {/each}
    </nav>
  </aside>

  <main class="settings-content">
    <Router basepath="/settings">
      <Route path="profile"><Profile /></Route>
      <Route path="api-keys"><ApiKeys /></Route>
      <Route path="sessions"><Sessions /></Route>
      <Route path="themes"><Themes /></Route>
      <Route path="users"><Users /></Route>
      <Route path="groups"><Groups /></Route>
      <Route path="roles"><Roles /></Route>
      <Route path="connections"><Connections /></Route>
      <Route path="connections/:id/share" let:params><ResourceShare kind="connections" id={params.id} /></Route>
      <Route path="notifications"><Notifications /></Route>
      <Route path="controls"><Controls /></Route>
      <Route path="memories"><Memories /></Route>
      <Route path="mqtt-devices"><MQTTDevices /></Route>
      <Route path="energy"><Energy /></Route>
      <Route path="history"><History /></Route>
      <Route path="plugins/:name" let:params>
        <PluginSettings name={params.name} />
      </Route>
      <Route path="agents"><Agents /></Route>
      <Route path="agents/:id/share" let:params><ResourceShare kind="agents" id={params.id} /></Route>
      <Route path="backup"><Backup /></Route>
      <Route path="event-log"><EventLog /></Route>
      <Route path="plugins"><Plugins /></Route>
      <Route path="/"><Overview /></Route>
    </Router>
  </main>
</div>

<style>
  /* Fills the scroll area <main> gives it rather than guessing the nav height. */
  .settings-layout {
    display: flex;
    min-height: 100%;
  }

  .sidebar {
    width: 240px;
    flex-shrink: 0;
    padding: 1rem 0;
    border-right: 1px solid var(--theme-border, var(--theme-surface0, #313244));
    background-color: var(--theme-nav-bg, var(--theme-mantle, #181825));
  }

  .sidebar-nav {
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 0 0.5rem;
  }

  .sidebar-item {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.6rem 1rem;
    border-radius: 4px;
    border-left: 3px solid transparent;
    font-size: 0.875rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
    background: none;
    text-align: left;
    width: 100%;
    text-decoration: none;
    transition: background-color 0.15s, border-left-color 0.15s;
  }

  .sidebar-item:hover {
    background-color: var(--theme-surface0, #313244);
  }

  .sidebar-item.active {
    background-color: var(--theme-surface0, #313244);
    color: var(--theme-accent, #89b4fa);
    border-left-color: var(--theme-accent, #89b4fa);
  }

  .sidebar-item i {
    font-size: 1.2rem;
    width: 1.4rem;
    text-align: center;
  }

  .settings-content {
    flex: 1;
    padding: 1.5rem 2rem;
    max-width: 960px;
  }
</style>
