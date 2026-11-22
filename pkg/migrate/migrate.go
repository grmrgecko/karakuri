package migrate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// AutoMigrate ensures all model tables exist, creating or altering them as needed.
func AutoMigrate() error {
	if err := relaxSlugIndexes(); err != nil {
		return err
	}
	if err := db.DB.AutoMigrate(models.All()...); err != nil {
		return err
	}
	return freezeAuthoringSettings()
}

// relaxSlugIndexes drops the unique slug indexes flows and dashboards were
// created with. Slugs are readable addresses resolved among what a caller can
// see, so two owners may use the same one. AutoMigrate recreates the index
// under the same name without the uniqueness, and would otherwise keep the
// existing unique one because the name matches. No rows change. It reads
// SQLite's pragma_index_list, so it does nothing on other engines.
func relaxSlugIndexes() error {
	if db.DB.Name() != "sqlite" {
		return nil
	}
	for _, table := range []string{"flows", "dashboards"} {
		var indexes []struct {
			Name   string
			Unique int
		}
		if err := db.DB.Raw("SELECT name, \"unique\" FROM pragma_index_list(?)", table).Scan(&indexes).Error; err != nil {
			return fmt.Errorf("reading %s indexes: %w", table, err)
		}
		for _, idx := range indexes {
			if idx.Name == "idx_"+table+"_slug" && idx.Unique == 1 {
				if err := db.DB.Exec("DROP INDEX " + idx.Name).Error; err != nil {
					return fmt.Errorf("dropping the unique %s slug index: %w", table, err)
				}
			}
		}
	}
	return nil
}

// SeedDefaults populates the default roles, groups, permissions, and role assignments so the system is usable on first boot.
// Uses Unscoped queries so soft-deleted records are found and skipped rather than duplicated.
// It runs in one transaction and fails on the first error: a role whose insert
// failed would otherwise seed permissions and assignments for id 0, and a later
// boot would find and keep them.
func SeedDefaults() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		// Create default roles.
		adminRole := models.Role{Name: "Admin", Description: "Full system access"}
		if err := tx.Unscoped().Where("name = ?", adminRole.Name).FirstOrCreate(&adminRole).Error; err != nil {
			return fmt.Errorf("seeding the Admin role: %w", err)
		}
		userRole := models.Role{Name: "User", Description: "Standard user access with ownership enforcement"}
		if err := tx.Unscoped().Where("name = ?", userRole.Name).FirstOrCreate(&userRole).Error; err != nil {
			return fmt.Errorf("seeding the User role: %w", err)
		}

		// Seed admin role permissions: full wildcard.
		if err := seedPermission(tx, adminRole.ID, "api:*"); err != nil {
			return err
		}

		// Seed user role permissions.
		userPerms := []string{
			"api:flows:read",
			"api:flows:write",
			"api:flows:deploy",
			"api:flows:trigger",
			"api:runs:read",
			"api:runs:cancel",
			"api:connections:read",
			"api:connections:invoke",
			"api:controls:read",
			"api:memories:read",
			"api:controls:set",
			"api:apikeys:read",
			"api:apikeys:write",
			"api:dashboards:read",
			"api:dashboards:write",
			"api:dashboards:deploy",
			"api:dashboards:delete",
			"api:dashboard-groups:read",
			"api:dashboard-groups:write",
			"api:dashboard-groups:delete",
			"api:renders:read",
			"api:renders:write",
			"api:renders:delete",
			"api:events:read",
		}
		for _, p := range userPerms {
			if err := seedPermission(tx, userRole.ID, p); err != nil {
				return err
			}
		}

		// Create default groups.
		adminGroup := models.UserGroup{Name: "Admin"}
		if err := tx.Unscoped().Where("name = ?", adminGroup.Name).FirstOrCreate(&adminGroup).Error; err != nil {
			return fmt.Errorf("seeding the Admin group: %w", err)
		}
		userGroup := models.UserGroup{Name: "User"}
		if err := tx.Unscoped().Where("name = ?", userGroup.Name).FirstOrCreate(&userGroup).Error; err != nil {
			return fmt.Errorf("seeding the User group: %w", err)
		}

		// Both default groups are owned by the Admin group.
		if err := seedGroupOwner(tx, adminGroup.ID, nil, &adminGroup.ID); err != nil {
			return err
		}
		if err := seedGroupOwner(tx, userGroup.ID, nil, &adminGroup.ID); err != nil {
			return err
		}

		// Assign roles to groups.
		if err := seedAssignment(tx, adminRole.ID, nil, &adminGroup.ID); err != nil {
			return err
		}
		return seedAssignment(tx, userRole.ID, nil, &userGroup.ID)
	})
}

// dashboardBackfillKey marks the one-shot dashboard deployment backfill as done.
const dashboardBackfillKey = "migrations.dashboard_versions_backfilled"

// BackfillDashboardDeployments deploys version 1 for dashboards that predate
// dashboard versioning, so an upgraded install keeps serving viewers the layout
// it served before. It runs once and records that in settings: a dashboard
// created after the upgrade must stay undeployed until its author deploys it.
func BackfillDashboardDeployments() error {
	if _, err := models.GetSetting(dashboardBackfillKey); err == nil {
		return nil
	}

	var dashboards []*models.Dashboard
	if err := db.DB.Find(&dashboards).Error; err != nil {
		return err
	}

	for _, d := range dashboards {
		if d.DeployedVersion != 0 {
			continue
		}
		widgets, err := models.ListDashboardWidgets(d.ID)
		if err != nil {
			return err
		}
		sections, err := models.ListDashboardSections(d.ID)
		if err != nil {
			return err
		}
		pages, err := models.ListDashboardPages(d.ID)
		if err != nil {
			return err
		}
		snapshot, err := models.SnapshotDashboard(d, widgets, sections, pages).JSON()
		if err != nil {
			return err
		}
		version := &models.DashboardVersion{
			DashboardID: d.ID,
			Version:     1,
			Snapshot:    snapshot,
			Comment:     "Layout carried over from before dashboard versioning",
			WidgetCount: len(widgets),
		}
		if err := db.DB.Create(version).Error; err != nil {
			return err
		}
		if err := db.DB.Model(&models.Dashboard{}).Where("id = ?", d.ID).
			Update("deployed_version", 1).Error; err != nil {
			return err
		}
	}

	return models.SetSetting(dashboardBackfillKey, "done")
}

// controlKindsBackfillKey marks the one-shot control kind backfill as done.
const controlKindsBackfillKey = "migrations.control_kinds_unified"

// BackfillControlKinds moves rows written before controls and MQTT devices
// shared one kind catalog. Devices used to store the widget face they were
// drawn with ("toggle", "slider") and kept their buttons in a column of their
// own; controls had a single unnamed "button" kind. Both now name a kind from
// controls.Kinds() and carry that kind's settings as JSON.
//
// It runs once and records that in settings, because a device saved after the
// upgrade is already in the new vocabulary and remapping it again would rename
// a kind an operator chose on purpose.
func BackfillControlKinds() error {
	if _, err := models.GetSetting(controlKindsBackfillKey); err == nil {
		return nil
	}
	if err := backfillDeviceKinds(); err != nil {
		return err
	}
	if err := backfillButtonControls(); err != nil {
		return err
	}
	if err := backfillNumberDisplay(); err != nil {
		return err
	}
	return models.SetSetting(controlKindsBackfillKey, "done")
}

// faceToKind maps the widget face an MQTT device used to store onto the kind
// that replaced it. A device drawn as a plain value becomes a text control,
// which reads as a value wherever it cannot be written, and a device cannot be
// written unless it has a payload script.
var faceToKind = map[string]string{
	"toggle": controls.KindBoolean,
	"slider": controls.KindNumber,
	"value":  controls.KindText,
	"button": controls.KindButtons,
}

// backfillDeviceKinds rewrites each device's control name and folds the old
// buttons column into the kind's settings. The column is read with raw SQL
// because the model no longer has the field, and GORM leaves a dropped column
// in place rather than deleting data.
func backfillDeviceKinds() error {
	var devices []*models.MQTTDevice
	if err := db.DB.Find(&devices).Error; err != nil {
		return err
	}

	for _, d := range devices {
		opts := d.OptionMap()
		if buttons := legacyDeviceButtons(d.ID); len(buttons) > 0 {
			opts["buttons"] = buttons
		}
		kind, remapped := faceToKind[d.Control]
		if !remapped {
			kind = d.Control
		}
		if _, ok := controls.FindKind(kind); !ok {
			// A face with no successor is still a device that reports; leaving
			// it unreadable would hide it from the panel that could fix it.
			kind = controls.KindText
		}
		d.Control = kind
		d.SetOptionMap(opts)
		if err := models.SaveMQTTDevice(d); err != nil {
			return err
		}
	}
	return nil
}

// legacyDeviceButtons reads the buttons column of a device saved before the
// kind settings existed, as the choices shape the catalog now uses. A missing
// column means the install never had one.
func legacyDeviceButtons(id uuid.UUID) []map[string]any {
	var raw string
	if err := db.DB.Raw("SELECT buttons FROM mqtt_devices WHERE id = ?", id).
		Scan(&raw).Error; err != nil {
		return nil
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var stored []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(stored))
	for _, b := range stored {
		if b.Label == "" && b.Value == "" {
			continue
		}
		out = append(out, map[string]any{"value": b.Value, "label": b.Label})
	}
	return out
}

// backfillButtonControls turns the old single-press control into a buttons
// control holding that one press. Its stored value was the time of the last
// press, which the new kind does not hold, so it is re-seeded.
func backfillButtonControls() error {
	var stored []*models.Control
	if err := db.DB.Where("kind = ?", "button").Find(&stored).Error; err != nil {
		return err
	}
	for _, c := range stored {
		opts := map[string]any{}
		if c.Options != "" {
			_ = json.Unmarshal([]byte(c.Options), &opts)
		}
		opts["buttons"] = []map[string]any{{"value": "press", "label": "Press"}}
		raw, err := json.Marshal(opts)
		if err != nil {
			return err
		}
		c.Kind = controls.KindButtons
		c.Options = string(raw)
		c.State = `""`
		if err := models.SaveControl(c); err != nil {
			return err
		}
	}
	return nil
}

// backfillNumberDisplay renames the number kind's "mode" setting to "display",
// which is what it is called now that several kinds choose how they draw. A
// control left with the old key would silently fall back to a slider even where
// its author had picked a number box.
func backfillNumberDisplay() error {
	var stored []*models.Control
	if err := db.DB.Where("kind = ?", controls.KindNumber).Find(&stored).Error; err != nil {
		return err
	}
	for _, c := range stored {
		opts := map[string]any{}
		if c.Options != "" {
			_ = json.Unmarshal([]byte(c.Options), &opts)
		}
		mode, ok := opts["mode"]
		if !ok {
			continue
		}
		delete(opts, "mode")
		if _, taken := opts["display"]; !taken {
			opts["display"] = mode
		}
		raw, err := json.Marshal(opts)
		if err != nil {
			return err
		}
		c.Options = string(raw)
		if err := models.SaveControl(c); err != nil {
			return err
		}
	}
	return nil
}

func seedPermission(tx *gorm.DB, roleID uint, permission string) error {
	rp := models.RolePermission{RoleID: roleID, Permission: permission}
	if err := tx.Unscoped().Where("role_id = ? AND permission = ?", roleID, permission).FirstOrCreate(&rp).Error; err != nil {
		return fmt.Errorf("seeding permission %s: %w", permission, err)
	}
	return nil
}

func seedGroupOwner(tx *gorm.DB, groupID uint, userID *uuid.UUID, ownerGroupID *uint) error {
	owner := models.GroupOwner{GroupID: groupID, UserID: userID, OwnerGroupID: ownerGroupID}
	var err error
	if ownerGroupID != nil {
		err = tx.Unscoped().Where("group_id = ? AND owner_group_id = ?", groupID, *ownerGroupID).FirstOrCreate(&owner).Error
	} else if userID != nil {
		err = tx.Unscoped().Where("group_id = ? AND user_id = ?", groupID, *userID).FirstOrCreate(&owner).Error
	}
	if err != nil {
		return fmt.Errorf("seeding the owner of group %d: %w", groupID, err)
	}
	return nil
}

func seedAssignment(tx *gorm.DB, roleID uint, userID *uuid.UUID, groupID *uint) error {
	ra := models.RoleAssignment{RoleID: roleID, UserID: userID, GroupID: groupID}
	var err error
	if groupID != nil {
		err = tx.Unscoped().Where("role_id = ? AND group_id = ?", roleID, *groupID).FirstOrCreate(&ra).Error
	} else if userID != nil {
		err = tx.Unscoped().Where("role_id = ? AND user_id = ?", roleID, *userID).FirstOrCreate(&ra).Error
	}
	if err != nil {
		return fmt.Errorf("seeding the assignment of role %d: %w", roleID, err)
	}
	return nil
}

// aiKindBackfillKey marks the one-shot AI connection kind unification as done.
const aiKindBackfillKey = "migrations.ai_provider_kind_unified"

// BackfillAIProviderKind moves connections written when each AI provider was
// its own kind onto the single "ai" kind, carrying the old kind name into the
// config's provider field. Runs once; a connection's ID never changes, so
// every node config referencing it keeps working.
func BackfillAIProviderKind() error {
	if _, err := models.GetSetting(aiKindBackfillKey); err == nil {
		return nil
	}

	oldKinds := []string{"openai", "openai-compatible", "anthropic", "gemini", "ollama"}
	var connections []*models.Connection
	if err := db.DB.Where("kind IN ?", oldKinds).Find(&connections).Error; err != nil {
		return err
	}
	for _, c := range connections {
		var cfg map[string]any
		if err := c.GetConfig(&cfg); err != nil {
			return fmt.Errorf("connection %s: reading config: %w", c.ID, err)
		}
		if cfg == nil {
			cfg = map[string]any{}
		}
		cfg["provider"] = c.Kind
		if err := c.SetConfig(cfg); err != nil {
			return fmt.Errorf("connection %s: rewriting config: %w", c.ID, err)
		}
		if err := db.DB.Model(&models.Connection{}).Where("id = ?", c.ID).
			Updates(map[string]any{"kind": "ai", "config": c.Config}).Error; err != nil {
			return err
		}
	}

	return models.SetSetting(aiKindBackfillKey, "done")
}

const assistResponsePortBackfillKey = "migrations.ha_assist_response_port_unified"

// BackfillAssistResponsePort moves the former Response, AI Stream, and
// Transcript return wires onto HA Assist's single Response socket.
func BackfillAssistResponsePort() error {
	if _, err := models.GetSetting(assistResponsePortBackfillKey); err == nil {
		return nil
	}
	var versions []*models.FlowVersion
	if err := db.DB.Find(&versions).Error; err != nil {
		return err
	}
	for _, version := range versions {
		if strings.TrimSpace(version.Graph) == "" {
			continue
		}
		var graph flow.Graph
		if err := json.Unmarshal([]byte(version.Graph), &graph); err != nil {
			return fmt.Errorf("flow version %d: reading graph: %w", version.ID, err)
		}
		if !rewriteAssistResponsePorts(&graph) {
			continue
		}
		raw, err := json.Marshal(graph)
		if err != nil {
			return fmt.Errorf("flow version %d: writing graph: %w", version.ID, err)
		}
		if err := db.DB.Model(&models.FlowVersion{}).Where("id = ?", version.ID).Update("graph", string(raw)).Error; err != nil {
			return err
		}
	}
	return models.SetSetting(assistResponsePortBackfillKey, "done")
}

func rewriteAssistResponsePorts(graph *flow.Graph) bool {
	assistNodes := make(map[string]bool)
	for _, node := range graph.Nodes {
		if node.Type == "ha-assist" {
			assistNodes[node.ID] = true
		}
	}
	rank := map[string]int{"response": 1, "transcript": 2, "stream": 3}
	chosen := make(map[string]int)
	chosenRank := make(map[string]int)
	for i, edge := range graph.Edges {
		if !assistNodes[edge.Target] || rank[edge.TargetHandle] == 0 {
			continue
		}
		if rank[edge.TargetHandle] > chosenRank[edge.Target] {
			chosen[edge.Target] = i
			chosenRank[edge.Target] = rank[edge.TargetHandle]
		}
	}
	changed := false
	edges := make([]flow.Edge, 0, len(graph.Edges))
	for i, edge := range graph.Edges {
		if !assistNodes[edge.Target] || rank[edge.TargetHandle] == 0 {
			edges = append(edges, edge)
			continue
		}
		if chosen[edge.Target] != i {
			changed = true
			continue
		}
		if edge.TargetHandle != "response" {
			edge.TargetHandle = "response"
			changed = true
		}
		edges = append(edges, edge)
	}
	graph.Edges = edges
	for id, subgraph := range graph.Subgraphs {
		if rewriteAssistResponsePorts(&subgraph) {
			graph.Subgraphs[id] = subgraph
			changed = true
		}
	}
	return changed
}

// freezeAuthoringSettings preserves the settings older versions read from live rows.
// Historical values were never stored, so the current values are the only baseline.
func freezeAuthoringSettings() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		var versions []models.FlowVersion
		if err := tx.Where("has_settings = ? AND version > 0", false).Find(&versions).Error; err != nil {
			return err
		}
		for _, v := range versions {
			var f models.Flow
			if err := tx.First(&f, "id = ?", v.FlowID).Error; err != nil {
				return err
			}
			if err := tx.Model(&v).Updates(map[string]any{"has_settings": true, "name": f.Name, "description": f.Description, "error_flow_id": f.ErrorFlowID}).Error; err != nil {
				return err
			}
		}
		var dashboards []models.DashboardVersion
		if err := tx.Find(&dashboards).Error; err != nil {
			return err
		}
		for _, v := range dashboards {
			snapshot, err := models.ParseDashboardSnapshot(v.Snapshot)
			if err != nil {
				return err
			}
			if snapshot.Presentation != nil {
				continue
			}
			var d models.Dashboard
			if err := tx.First(&d, "id = ?", v.DashboardID).Error; err != nil {
				return err
			}
			snapshot.Presentation = &models.DashboardPresentation{Name: d.Name, Description: d.Description, ThemeID: d.ThemeID}
			raw, err := snapshot.JSON()
			if err != nil {
				return err
			}
			if err := tx.Model(&v).Update("snapshot", raw).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
