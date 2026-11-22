package permissions

// Permission string constants for API authorization gates.
const (
	PermUsersRead  = "api:users:read"
	PermUsersWrite = "api:users:write"

	PermGroupsRead  = "api:groups:read"
	PermGroupsWrite = "api:groups:write"

	PermRolesRead  = "api:roles:read"
	PermRolesWrite = "api:roles:write"

	PermAPIKeysRead  = "api:apikeys:read"
	PermAPIKeysWrite = "api:apikeys:write"

	PermFlowsRead   = "api:flows:read"
	PermFlowsWrite  = "api:flows:write"
	PermFlowsDeploy = "api:flows:deploy"
	// PermFlowsTrigger covers firing a flow's named widget triggers without any
	// other flow access, which is what a dashboard button needs.
	PermFlowsTrigger = "api:flows:trigger"
	// PermFlowsManage covers acting on a flow the caller neither owns nor holds
	// a share on. Read, write, and deploy say what a user may do with their own
	// flows and the ones shared with them; manage reaches into everyone's.
	PermFlowsManage = "api:flows:manage"

	PermRunsRead   = "api:runs:read"
	PermRunsCancel = "api:runs:cancel"

	PermConnectionsRead  = "api:connections:read"
	PermConnectionsWrite = "api:connections:write"
	// PermConnectionsInvoke covers acting through a connection without
	// reconfiguring it, such as a dashboard widget calling a Home Assistant
	// service.
	PermConnectionsInvoke = "api:connections:invoke"
	// PermConnectionsManage covers acting on a connection the caller neither
	// owns nor holds a share on, as flows manage does for flows.
	PermConnectionsManage = "api:connections:manage"

	PermControlsRead  = "api:controls:read"
	PermControlsWrite = "api:controls:write"
	// PermControlsSet covers operating a control without being able to define
	// one, which is what a dashboard row needs.
	PermControlsSet = "api:controls:set"

	PermMemoriesRead  = "api:memories:read"
	PermMemoriesWrite = "api:memories:write"

	PermDashboardsRead  = "api:dashboards:read"
	PermDashboardsWrite = "api:dashboards:write"
	// PermDashboardsDeploy covers publishing a layout to viewers, which editing
	// the draft no longer does on its own.
	PermDashboardsDeploy = "api:dashboards:deploy"
	PermDashboardsDelete = "api:dashboards:delete"
	// PermDashboardsManage covers acting on a dashboard the caller neither owns
	// nor holds a share on. Write, deploy, and delete say what a user may do
	// with their own dashboards and the ones shared with them, so every account
	// holds those; manage is what reaches into somebody else's.
	PermDashboardsManage = "api:dashboards:manage"

	PermDashboardGroupsRead   = "api:dashboard-groups:read"
	PermDashboardGroupsWrite  = "api:dashboard-groups:write"
	PermDashboardGroupsDelete = "api:dashboard-groups:delete"
	// PermDashboardGroupsManage is manage for dashboard groups.
	PermDashboardGroupsManage = "api:dashboard-groups:manage"

	// PermRendersRead covers listing a dashboard's renders and schedules,
	// write covers asking for one, and delete covers cancelling a job or
	// removing a schedule. Being able to view the dashboard is required on top
	// of all three: a render shows exactly what the dashboard shows.
	PermRendersRead   = "api:renders:read"
	PermRendersWrite  = "api:renders:write"
	PermRendersDelete = "api:renders:delete"

	PermThemesWrite  = "api:themes:write"
	PermThemesDelete = "api:themes:delete"

	PermPluginsRead = "api:plugins:read"
	// PermPluginsWrite covers loading, unloading, enabling, and editing a
	// plugin's settings — everything that changes what the host is running.
	PermPluginsWrite = "api:plugins:write"

	PermAgentsRead = "api:agents:read"
	// PermAgentsWrite covers renaming, describing, enabling, and deleting an
	// agent record.
	PermAgentsWrite = "api:agents:write"
	// PermAgentsEnroll covers minting enrollment tokens, the credential that
	// lets a new host join.
	PermAgentsEnroll = "api:agents:enroll"
	// PermAgentsRevoke covers revoking an agent's certificate, cutting off a
	// host whose key may be compromised.
	PermAgentsRevoke = "api:agents:revoke"
	// PermAgentsManage covers acting on an agent the caller neither owns nor
	// holds a share on, including the enrollment tokens others minted.
	PermAgentsManage = "api:agents:manage"

	// PermBackupsRead covers listing archives and runs and reading the
	// schedule; write covers creating, verifying, and deleting archives and
	// changing the schedule. Download is separate: an archive and its
	// passphrase hand over every stored credential and the agent CA.
	PermBackupsRead     = "api:backups:read"
	PermBackupsWrite    = "api:backups:write"
	PermBackupsDownload = "api:backups:download"

	// PermEventsRead covers the event log except security entries; audit
	// adds security entries; write covers the log's settings.
	PermEventsRead  = "api:events:read"
	PermEventsAudit = "api:events:audit"
	PermEventsWrite = "api:events:write"

	// PermMCPUse gates the MCP endpoint itself: an administrator decides which
	// groups may drive the server over MCP at all. Each tool still checks its
	// own api:* permission, so this is the door and those are the locks.
	PermMCPUse = "api:mcp:use"
)
