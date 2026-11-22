package mcpserver

import (
	"context"
	"strings"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pluginHost and pluginManager are wired by the server at startup, beside the
// api package's own copies: the HTTP proxy and the MCP tools each hold what
// they call. Nil in tests that never touch plugins.
var (
	pluginHost    *pluginhost.Host
	pluginManager *pluginhost.Manager
)

// SetPlugins installs the plugin host and manager used by the plugin tools.
func SetPlugins(h *pluginhost.Host, m *pluginhost.Manager) {
	pluginHost = h
	pluginManager = m
}

type pluginNameInput struct {
	Name string `json:"name" jsonschema:"Plugin name, from list_plugins."`
}

type setPluginEnabledInput struct {
	Name    string `json:"name" jsonschema:"Plugin name, from list_plugins."`
	Enabled bool   `json:"enabled" jsonschema:"true loads the plugin and remembers it for the next start; false unloads it and keeps it unloaded."`
}

type updatePluginSettingsInput struct {
	Name   string            `json:"name" jsonschema:"Plugin name, from list_plugins."`
	Values map[string]string `json:"values" jsonschema:"Setting values by key, as the plugin's settings schema declares them."`
}

// addPluginTools registers the plugin management tools.
func addPluginTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_plugins",
		Description: "List every plugin with its load state and what it contributes: nodes, connection kinds, widgets.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermPluginsRead); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"plugins": pluginhost.Inventory(pluginHost, pluginManager)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_plugin_enabled",
		Description: "Enable or disable a plugin. The choice persists across restarts; built-ins refuse to unload.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in setPluginEnabledInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermPluginsWrite); err != nil {
			return nil, nil, err
		}
		if pluginManager == nil {
			return nil, nil, opErr(apperrors.ErrServiceUnavailable.WithMessage("plugin manager is not available"))
		}
		if err := pluginManager.SetEnabled(in.Name, in.Enabled); err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage(err.Error()))
		}
		return nil, map[string]any{"name": in.Name, "enabled": in.Enabled}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_plugin_settings",
		Description: "Get a plugin's settings schema and its stored values.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pluginNameInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermPluginsRead); err != nil {
			return nil, nil, err
		}
		if pluginManager == nil {
			return nil, nil, opErr(apperrors.ErrServiceUnavailable.WithMessage("plugin manager is not available"))
		}
		prefix := "plugins." + in.Name + "."
		settings, err := models.ListSettingsByPrefix(prefix)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		values := make(map[string]string, len(settings))
		for _, setting := range settings {
			values[strings.TrimPrefix(setting.Key, prefix)] = setting.Value
		}
		return nil, map[string]any{
			"schema": pluginManager.SettingsSchema(in.Name),
			"values": values,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_plugin_settings",
		Description: "Store a plugin's settings and reload it so they take effect: a plugin reads its configuration once, at Init.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updatePluginSettingsInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermPluginsWrite); err != nil {
			return nil, nil, err
		}
		for key, value := range in.Values {
			if err := models.SetSetting("plugins."+in.Name+"."+key, value); err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
		}
		if pluginManager != nil && pluginManager.IsLoaded(in.Name) {
			if err := pluginManager.ReloadPlugin(in.Name); err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithMessage("settings saved but reload failed: " + err.Error()))
			}
		}
		return nil, map[string]any{"saved": true}, nil
	})
}
