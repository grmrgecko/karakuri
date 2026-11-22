package mcpserver_test

import (
	"context"
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetFlowHidesAFolderTheCallerCannotSee pins that a flow shared out of
// someone else's folder is reported without that folder, and with the caller's
// level, over MCP as over REST. list_flows masked the folder, but get_flow and
// the other tools returning a flow sent the stored folder ID to a caller who
// holds nothing on the folder.
func TestGetFlowHidesAFolderTheCallerCannotSee(t *testing.T) {
	srv, _, gatedToken := setupMCPServer(t)

	var owner, gated models.User
	require.NoError(t, db.DB.Where("username = ?", "mcp-admin").First(&owner).Error, "the owner fixture must exist")
	require.NoError(t, db.DB.Where("username = ?", "mcp-gated").First(&gated).Error, "the gated fixture must exist")
	var role models.Role
	require.NoError(t, db.DB.Where("name = ?", "mcp-gate-only").First(&role).Error, "the gated user's role must exist")
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: permissions.PermFlowsRead}).Error,
		"the gated user must be allowed to read flows at all")

	folder := &models.Folder{Name: "Garage", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(folder).Error, "the owner's folder must be stored")
	f, err := flowops.Create(context.Background(), "Garage door", "", false, &folder.ID, owner.ID)
	require.NoError(t, err, "the owner's flow must be created in the folder")
	require.NoError(t, db.DB.Create(&models.FlowAccess{FlowID: f.ID, UserID: &gated.ID, Permission: models.FlowAccessView}).Error,
		"the flow alone must be shared with the gated user")

	res, out := callTool(t, connectMCP(t, srv, gatedToken), "get_flow", map[string]any{"id": f.ID.String()})
	require.Falsef(t, res.IsError, "a view share must open the flow: %v", res.Content)
	assert.Equal(t, f.ID.String(), out["id"], "the tool must answer with the shared flow")
	assert.NotContains(t, out, "folder_id", "a folder the caller holds nothing on must not be named")
	assert.Equal(t, models.FlowAccessView, out["access_level"], "the caller's level tells an agent what it may do with the flow")
}
