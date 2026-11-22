package models

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a name into a URL-safe slug by lowercasing, replacing
// non-alphanumeric characters with hyphens, and trimming leading/trailing hyphens.
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// Flow is a stored automation graph. The editable draft and the immutable
// deployed graph are separate FlowVersion rows referenced by ID.
type Flow struct {
	ID                uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name              string     `gorm:"not null" json:"name" description:"Display name."`
	// Slug is a readable address and the flow's metrics label. It need not be
	// unique: a slug resolves among the flows the caller can read, and the ID
	// is canonical.
	Slug              string     `gorm:"index;not null" json:"slug" description:"Readable address; not unique. The ID is canonical."`
	Description       string     `json:"description" description:"Optional summary."`
	IsSubflow         bool       `gorm:"default:false" json:"is_subflow" description:"Whether this flow is a reusable subflow."`
	FolderID          *uint      `gorm:"index" json:"folder_id,omitempty" description:"Containing folder ID; absent at the root."`
	Enabled           bool       `gorm:"default:false" json:"enabled" description:"Whether the deployed flow is active."`
	OwnerID           uuid.UUID  `gorm:"type:uuid;not null" json:"owner_id" description:"Owner user ID."`
	// HasFaceplates records whether the deployed version holds a faceplate,
	// so flows shared at faceplate level can be listed without reading graphs.
	HasFaceplates bool `gorm:"not null;default:false" json:"has_faceplates" description:"Whether the deployed version has faceplate tabs."`
	Owner             User       `gorm:"foreignKey:OwnerID" json:"-"`
	DraftVersionID    *uint      `json:"draft_version_id,omitempty" description:"Editable draft version ID."`
	DeployedVersionID *uint      `json:"deployed_version_id,omitempty" description:"Currently deployed version ID."`
	ErrorFlowID       *uuid.UUID `gorm:"type:uuid" json:"error_flow_id,omitempty" description:"Flow triggered when this flow fails."`
	CreatedAt         time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt         time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID and generates a slug from the name if not set.
func (f *Flow) BeforeCreate(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	if f.Slug == "" {
		f.Slug = Slugify(f.Name)
	}
	return nil
}

// FlowsWithSlug returns every flow using a slug, with its owner, for callers
// that filter them by what a user may read.
func FlowsWithSlug(slug string) ([]*Flow, error) {
	var out []*Flow
	err := db.DB.Preload("Owner").Where("slug = ?", slug).Order("name ASC").Find(&out).Error
	return out, err
}

// Delete removes the flow with its versions and dependencies in a transaction.
func (f *Flow) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("flow_id = ?", f.ID).Delete(&FlowVersion{}).Error; err != nil {
			return err
		}
		if err := tx.Where("flow_id = ?", f.ID).Delete(&FlowDependency{}).Error; err != nil {
			return err
		}
		// The last run and its events belong to the flow too.
		var runIDs []uuid.UUID
		if err := tx.Model(&Run{}).Where("flow_id = ?", f.ID).Pluck("id", &runIDs).Error; err != nil {
			return err
		}
		if len(runIDs) > 0 {
			if err := tx.Where("run_id IN ?", runIDs).Delete(&RunEvent{}).Error; err != nil {
				return err
			}
			if err := tx.Where("flow_id = ?", f.ID).Delete(&Run{}).Error; err != nil {
				return err
			}
		}
		if err := DeleteFlowVariables(tx, f.ID); err != nil {
			return err
		}
		if err := DeleteFlowNodeStore(tx, f.ID); err != nil {
			return err
		}
		if err := DeleteFlowPendingWaits(tx, f.ID); err != nil {
			return err
		}
		if err := tx.Where("flow_id = ?", f.ID).Delete(&FlowAccess{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Flow{}, "id = ?", f.ID).Error
	})
}

// FindFlowByID returns a flow looked up by ID.
func FindFlowByID(id uuid.UUID) (*Flow, error) {
	var flow Flow
	err := db.DB.Where("id = ?", id).First(&flow).Error
	return &flow, err
}

// ListFlows returns a paginated list of flows ordered by name, optionally
// filtered by the is_subflow flag.
func ListFlows(page, pageSize int, isSubflow *bool) ([]*Flow, int64, error) {
	var flows []*Flow
	var count int64

	query := db.DB.Model(&Flow{})
	if isSubflow != nil {
		query = query.Where("is_subflow = ?", *isSubflow)
	}
	query.Count(&count)

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("name ASC").Find(&flows).Error
	return flows, count, err
}

// ListDeployedEnabledFlows returns every flow that is enabled and has a
// deployed version, for trigger arming at startup. Subflows are excluded:
// they have no triggers and only run inside a parent flow.
func ListDeployedEnabledFlows() ([]*Flow, error) {
	var flows []*Flow
	err := db.DB.Where("enabled = ? AND deployed_version_id IS NOT NULL AND is_subflow = ?", true, false).Find(&flows).Error
	return flows, err
}

// ListSubflowDependents returns the flows whose dependency rows reference the
// given shared subflow.
func ListSubflowDependents(subflowID uuid.UUID) ([]*Flow, error) {
	var flows []*Flow
	err := db.DB.
		Joins("JOIN flow_dependencies ON flow_dependencies.flow_id = flows.id").
		Where("flow_dependencies.kind = ? AND flow_dependencies.ref_id = ?", "subflow", subflowID.String()).
		Find(&flows).Error
	return flows, err
}

// ReplaceFlowDependencies rebuilds the dependency rows for a flow in a transaction.
func ReplaceFlowDependencies(flowID uuid.UUID, deps []FlowDependency) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("flow_id = ?", flowID).Delete(&FlowDependency{}).Error; err != nil {
			return err
		}
		for i := range deps {
			deps[i].FlowID = flowID
			if err := tx.Create(&deps[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
