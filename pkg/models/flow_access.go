package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Flow access levels, ordered least to most privileged. A grant of one level
// implies every level below it. Faceplate reads and applies a flow's
// faceplates without the graph; view reads the graph; edit changes, tests,
// deploys, and runs it; admin also shares and deletes it.
const (
	FlowAccessFaceplate = "faceplate"
	FlowAccessView      = "view"
	FlowAccessEdit      = "edit"
	FlowAccessAdmin     = "admin"
)

// flowAccessRank orders the levels so a comparison decides whether a grant
// satisfies a request.
var flowAccessRank = map[string]int{
	FlowAccessFaceplate: 1,
	FlowAccessView:      2,
	FlowAccessEdit:      3,
	FlowAccessAdmin:     4,
}

// ValidFlowAccess reports whether level names a flow access level.
func ValidFlowAccess(level string) bool {
	_, ok := flowAccessRank[level]
	return ok
}

// FlowAccessRank returns a level's rank, 0 for an unknown level.
func FlowAccessRank(level string) int {
	return flowAccessRank[level]
}

// FlowAccessLevel returns the level name for a rank, or "" for 0.
func FlowAccessLevel(rank int) string {
	for level, r := range flowAccessRank {
		if r == rank {
			return level
		}
	}
	return ""
}

// FlowAccess grants one access level on one flow to a user or a user group.
// Shares sit outside versions: restoring a version changes none of them.
type FlowAccess struct {
	ID         uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	FlowID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"flow_id" description:"Flow ID."`
	UserID     *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty" description:"Granted user ID."`
	User       *User      `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Granted user."`
	GroupID    *uint      `gorm:"index" json:"group_id,omitempty" description:"Granted user group ID."`
	Group      *UserGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Granted user group."`
	Permission string     `gorm:"not null" json:"permission" description:"Access level: faceplate, view, edit, or admin."`
	CreatedAt  time.Time  `json:"created_at" description:"Creation timestamp."`
}

// FlowGrantRank returns the highest access rank a user holds on a flow through
// ownership or shares, direct or through any of their user groups, or through
// the folder holding the flow and that folder's ancestors, and 0 when they hold
// none. The owner of the flow or of a folder above it ranks as admin. Blanket
// permissions are the caller's concern.
func FlowGrantRank(flow *Flow, userID uuid.UUID, userGroupIDs []uint) (int, error) {
	if userID == uuid.Nil {
		return 0, nil
	}
	if flow.OwnerID == userID {
		return flowAccessRank[FlowAccessAdmin], nil
	}
	inherited := 0
	if flow.FolderID != nil {
		ranks, err := FolderRanks(userID, userGroupIDs)
		if err != nil {
			return 0, err
		}
		inherited = ranks[*flow.FolderID]
	}
	var grants []FlowAccess
	q := db.DB.Where("flow_id = ? AND user_id = ?", flow.ID, userID)
	if len(userGroupIDs) > 0 {
		q = q.Or("flow_id = ? AND group_id IN ?", flow.ID, userGroupIDs)
	}
	if err := q.Find(&grants).Error; err != nil {
		return 0, err
	}
	held := inherited
	for _, g := range grants {
		held = max(held, flowAccessRank[g.Permission])
	}
	return held, nil
}

// accessibleFlowsAtLeast restricts a flow query to flows the user owns or
// holds a share of at least minLevel on, directly, through a group, or through
// a folder above the flow.
func accessibleFlowsAtLeast(query *gorm.DB, userID uuid.UUID, userGroupIDs []uint, minLevel string) (*gorm.DB, error) {
	ranks, err := FolderRanks(userID, userGroupIDs)
	if err != nil {
		return nil, err
	}
	folders := foldersAtLeast(ranks, minLevel)
	if len(folders) == 0 {
		folders = []uint{0}
	}
	levels := make([]string, 0, len(flowAccessRank))
	for level, rank := range flowAccessRank {
		if rank >= flowAccessRank[minLevel] {
			levels = append(levels, level)
		}
	}
	shared := db.DB.Model(&FlowAccess{}).Select("flow_id").
		Where("permission IN ?", levels).
		Where(db.DB.Where("user_id = ?", userID).Or("group_id IN ?", groupIDsOrNone(userGroupIDs)))
	return query.Where("owner_id = ? OR id IN (?) OR folder_id IN ?", userID, shared, folders), nil
}

// groupIDsOrNone returns the IDs, or a value no group has, so an IN clause
// over an empty list stays valid SQL.
func groupIDsOrNone(ids []uint) []uint {
	if len(ids) == 0 {
		return []uint{0}
	}
	return ids
}

// ListAccessibleFlows returns a page of the flows a user owns or holds a share
// of at least minLevel on, ordered by name, optionally filtered by the
// is_subflow flag.
func ListAccessibleFlows(userID uuid.UUID, userGroupIDs []uint, minLevel string, page, pageSize int, isSubflow *bool) ([]*Flow, int64, error) {
	var flows []*Flow
	var count int64

	query, err := accessibleFlowsAtLeast(db.DB.Model(&Flow{}), userID, userGroupIDs, minLevel)
	if err != nil {
		return nil, 0, err
	}
	if isSubflow != nil {
		query = query.Where("is_subflow = ?", *isSubflow)
	}
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err = query.Offset(offset).Limit(pageSize).Order("name ASC").Find(&flows).Error
	return flows, count, err
}

// AccessibleFlowIDs returns the IDs of every flow a user owns or holds a share
// of at least minLevel on.
func AccessibleFlowIDs(userID uuid.UUID, userGroupIDs []uint, minLevel string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	query, err := accessibleFlowsAtLeast(db.DB.Model(&Flow{}), userID, userGroupIDs, minLevel)
	if err != nil {
		return nil, err
	}
	err = query.Pluck("id", &ids).Error
	return ids, err
}

// ListFlowAccess returns a flow's shares with their users and groups.
func ListFlowAccess(flowID uuid.UUID) ([]FlowAccess, error) {
	var entries []FlowAccess
	err := db.DB.Preload("User").Preload("Group").Where("flow_id = ?", flowID).Order("id ASC").Find(&entries).Error
	return entries, err
}
