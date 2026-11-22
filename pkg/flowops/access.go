package flowops

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
)

// Rank returns the access rank a user holds on a flow: admin for its owner
// and for a holder of the manage permission, otherwise the highest share held
// directly or through a group, and 0 for none. An anonymous caller holds
// nothing.
func Rank(ctx context.Context, userID uuid.UUID, f *models.Flow) int {
	if userID == uuid.Nil {
		return 0
	}
	if userID == f.OwnerID || auth.Can(ctx, userID, perm.PermFlowsManage) {
		return models.FlowAccessRank(models.FlowAccessAdmin)
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	rank, err := models.FlowGrantRank(f, userID, groupIDs)
	if err != nil {
		return 0
	}
	return rank
}

// Level returns the access level a user holds on a flow, or "" for none.
func Level(ctx context.Context, userID uuid.UUID, f *models.Flow) string {
	return models.FlowAccessLevel(Rank(ctx, userID, f))
}

// Holds reports whether a user holds at least level on a flow.
func Holds(ctx context.Context, userID uuid.UUID, f *models.Flow, level string) bool {
	return Rank(ctx, userID, f) >= models.FlowAccessRank(level)
}

// Authorize answers whether a user may act on a loaded flow at level. A user
// holding nothing on it is told it does not exist, so private flow IDs cannot
// be confirmed by probing; one holding too little is refused.
func Authorize(ctx context.Context, userID uuid.UUID, f *models.Flow, level string) error {
	rank := Rank(ctx, userID, f)
	if rank == 0 {
		return apperrors.ErrNotFound.WithMessage("flow not found")
	}
	if rank < models.FlowAccessRank(level) {
		return apperrors.ErrForbidden.WithMessage("your access to this flow does not allow that")
	}
	return nil
}

// LoadFor loads a flow and authorizes the user on it at level.
func LoadFor(ctx context.Context, userID uuid.UUID, id string, level string) (*models.Flow, error) {
	f, err := Load(id)
	if err != nil {
		return nil, err
	}
	if err := Authorize(ctx, userID, f, level); err != nil {
		return nil, err
	}
	return f, nil
}

// LoadByIDFor is LoadFor for an already parsed flow ID.
func LoadByIDFor(ctx context.Context, userID uuid.UUID, id uuid.UUID, level string) (*models.Flow, error) {
	f, err := models.FindFlowByID(id)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("flow not found")
	}
	if err := Authorize(ctx, userID, f, level); err != nil {
		return nil, err
	}
	return f, nil
}

// ListFor returns a page of the flows a user may open: every flow for a holder
// of the manage permission, otherwise those they own or were shared at view or
// above, plus those shared at faceplate level whose deployed version has
// faceplates to show.
func ListFor(ctx context.Context, userID uuid.UUID, page, pageSize int, isSubflow *bool) ([]*models.Flow, int64, error) {
	if auth.Can(ctx, userID, perm.PermFlowsManage) {
		return models.ListFlows(page, pageSize, isSubflow)
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	viewable, err := models.AccessibleFlowIDs(userID, groupIDs, models.FlowAccessView)
	if err != nil {
		return nil, 0, err
	}
	faceplate, err := models.AccessibleFlowIDs(userID, groupIDs, models.FlowAccessFaceplate)
	if err != nil {
		return nil, 0, err
	}
	ids := append([]uuid.UUID{uuid.Nil}, viewable...)
	var withFaceplates []uuid.UUID
	if len(faceplate) > 0 {
		if err := db.DB.Model(&models.Flow{}).Where("id IN ? AND has_faceplates = ?", faceplate, true).Pluck("id", &withFaceplates).Error; err != nil {
			return nil, 0, err
		}
	}
	ids = append(ids, withFaceplates...)

	query := db.DB.Model(&models.Flow{}).Where("id IN ?", ids)
	if isSubflow != nil {
		query = query.Where("is_subflow = ?", *isSubflow)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var flows []*models.Flow
	err = query.Offset((page - 1) * pageSize).Limit(pageSize).Order("name ASC").Find(&flows).Error
	return flows, count, err
}

// ReadableFlowIDs returns the set of flow IDs a user may read, or nil when
// they hold the manage permission and may read every flow.
func ReadableFlowIDs(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]bool, error) {
	if auth.Can(ctx, userID, perm.PermFlowsManage) {
		return nil, nil
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ids, err := models.AccessibleFlowIDs(userID, groupIDs, models.FlowAccessView)
	if err != nil {
		return nil, err
	}
	set := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// RunFlowID returns the flow a run belongs to, from the engine while the run
// is active and from the runs table afterwards.
func RunFlowID(runID uuid.UUID) (uuid.UUID, bool) {
	if eng != nil {
		for _, snap := range eng.ActiveRuns() {
			if snap.ID == runID {
				return snap.FlowID, true
			}
		}
	}
	var run models.Run
	if err := db.DB.Select("flow_id").Where("id = ?", runID).First(&run).Error; err != nil {
		return uuid.Nil, false
	}
	return run.FlowID, true
}

// CancelRunFor cancels an active run when the user holds edit on its flow. A
// run of a flow they hold nothing on is reported as not active.
func CancelRunFor(ctx context.Context, userID uuid.UUID, runID uuid.UUID) error {
	flowID, ok := RunFlowID(runID)
	if !ok {
		return apperrors.ErrNotFound.WithMessage("no active run with this ID")
	}
	f, err := models.FindFlowByID(flowID)
	if err != nil || Rank(ctx, userID, f) == 0 {
		return apperrors.ErrNotFound.WithMessage("no active run with this ID")
	}
	if err := Authorize(ctx, userID, f, models.FlowAccessEdit); err != nil {
		return err
	}
	return CancelRun(runID)
}

// ActiveRunsFor returns the active runs of the flows a user may read.
func ActiveRunsFor(ctx context.Context, userID uuid.UUID) ([]engine.RunSnapshot, error) {
	runs, err := ActiveRuns()
	if err != nil {
		return nil, err
	}
	readable, err := ReadableFlowIDs(ctx, userID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	if readable == nil {
		return runs, nil
	}
	out := make([]engine.RunSnapshot, 0, len(runs))
	for _, r := range runs {
		if readable[r.FlowID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// sharedSubflowRefs collects the shared subflow IDs a graph references, in
// its root and every local subgraph.
func sharedSubflowRefs(g *flow.Graph, into map[uuid.UUID]bool) {
	if g == nil {
		return
	}
	for _, n := range g.Nodes {
		if n.SubflowID != nil {
			into[*n.SubflowID] = true
		}
	}
	for _, sub := range g.Subgraphs {
		sharedSubflowRefs(&sub, into)
	}
}

// CheckSubflowReferences refuses a draft that adds a reference to a shared
// subflow the user may not read. References the current draft already holds
// stay, so a flow never becomes unsaveable because its author lost access to
// a subflow it calls.
func CheckSubflowReferences(ctx context.Context, userID uuid.UUID, f *models.Flow, g *flow.Graph) error {
	next := map[uuid.UUID]bool{}
	sharedSubflowRefs(g, next)
	if len(next) == 0 {
		return nil
	}
	current := map[uuid.UUID]bool{}
	if draft, err := LoadDraft(f); err == nil {
		if cg, err := ParseGraph(draft.Graph); err == nil {
			sharedSubflowRefs(cg, current)
		}
	}
	for id := range next {
		if current[id] {
			continue
		}
		sf, err := models.FindFlowByID(id)
		if err != nil || !Holds(ctx, userID, sf, models.FlowAccessView) {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("shared subflow %s not found", id))
		}
	}
	return nil
}

// CanWatchFlow answers a WebSocket subscription to a flow's topic: the socket's
// user must hold permission and view on the flow. user is the socket's user
// ID, empty for an anonymous socket.
func CanWatchFlow(user, flowID, permission string) bool {
	uid, err := uuid.Parse(user)
	if err != nil {
		return false
	}
	fid, err := uuid.Parse(flowID)
	if err != nil {
		return false
	}
	if !auth.HasPermission(uid, permission) {
		return false
	}
	f, err := models.FindFlowByID(fid)
	// A socket never authenticates with an API key, so no key limit applies.
	return err == nil && Holds(context.Background(), uid, f, models.FlowAccessView)
}

// CanWatchRun answers a subscription to one run's live node states and debug
// messages, which follow the run's flow.
func CanWatchRun(user, runID, permission string) bool {
	rid, err := uuid.Parse(runID)
	if err != nil {
		return false
	}
	flowID, ok := RunFlowID(rid)
	if !ok {
		return false
	}
	return CanWatchFlow(user, flowID.String(), permission)
}

// EventVisible reports whether a user may see one event log entry: anything
// that is not about a flow or folder, or an entry about a flow or folder they
// may read.
func EventVisible(ctx context.Context, userID uuid.UUID, subjectKind, subjectID string) bool {
	if !access.EventVisible(ctx, userID, subjectKind, subjectID) {
		return false
	}
	if subjectKind == "folder" {
		id, err := strconv.ParseUint(subjectID, 10, 64)
		return err == nil && FolderRank(ctx, userID, uint(id)) >= models.FlowAccessRank(models.FlowAccessView)
	}
	if subjectKind != "flow" {
		return true
	}
	fid, err := uuid.Parse(subjectID)
	if err != nil {
		return false
	}
	f, err := models.FindFlowByID(fid)
	if err != nil {
		// A deleted flow has no shares left to decide by, so its entries stay
		// with whoever may read every flow.
		return auth.Can(ctx, userID, perm.PermFlowsManage)
	}
	return Holds(ctx, userID, f, models.FlowAccessView)
}

// LimitEventFlows narrows an event log query to the flow entries a user may
// read. A holder of the manage permission reads every flow's entries.
func LimitEventFlows(ctx context.Context, userID uuid.UUID, f *events.Filter) error {
	if err := access.LimitEvents(ctx, userID, f); err != nil {
		return err
	}
	readable, err := ReadableFlowIDs(ctx, userID)
	if err != nil {
		return err
	}
	if readable == nil {
		return nil
	}
	f.LimitFlows = true
	f.ReadableFlows = make([]string, 0, len(readable))
	for id := range readable {
		f.ReadableFlows = append(f.ReadableFlows, id.String())
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ranks, err := models.FolderRanks(userID, groupIDs)
	if err != nil {
		return err
	}
	for id, rank := range ranks {
		if rank >= models.FlowAccessRank(models.FlowAccessView) {
			f.ReadableFolders = append(f.ReadableFolders, strconv.FormatUint(uint64(id), 10))
		}
	}
	return nil
}

// FolderRank returns the access rank a user holds on a folder: admin for a
// holder of the manage permission, otherwise what the folder's owner and
// shares along its path grant them.
func FolderRank(ctx context.Context, userID uuid.UUID, folderID uint) int {
	if userID == uuid.Nil {
		return 0
	}
	if auth.Can(ctx, userID, perm.PermFlowsManage) {
		return models.FlowAccessRank(models.FlowAccessAdmin)
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ranks, err := models.FolderRanks(userID, groupIDs)
	if err != nil {
		return 0
	}
	return ranks[folderID]
}

// AuthorizeFolder answers whether a user may act on a folder at level. A
// folder they hold nothing on is reported as missing.
func AuthorizeFolder(ctx context.Context, userID uuid.UUID, folderID uint, level string) (*models.Folder, error) {
	folder, err := models.FindFolderByID(folderID)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("folder not found")
	}
	rank := FolderRank(ctx, userID, folderID)
	if rank == 0 {
		return nil, apperrors.ErrNotFound.WithMessage("folder not found")
	}
	if rank < models.FlowAccessRank(level) {
		return nil, apperrors.ErrForbidden.WithMessage("your access to this folder does not allow that")
	}
	return folder, nil
}

// CheckFolderTarget answers whether a user may place something in a folder,
// where nil is the root. A folder's owner and shares reach what it holds, so
// putting a flow or folder there is a sharing change and needs edit on it.
func CheckFolderTarget(ctx context.Context, userID uuid.UUID, folderID *uint) error {
	if folderID == nil {
		return nil
	}
	if _, err := AuthorizeFolder(ctx, userID, *folderID, models.FlowAccessEdit); err != nil {
		return apperrors.ErrValidation.WithMessage("folder not found, or you may not add to it")
	}
	return nil
}

// ListFoldersFor returns the folders a user may see, each carrying their
// level: every folder for a holder of the manage permission, otherwise those
// ranked view or above.
func ListFoldersFor(ctx context.Context, userID uuid.UUID) ([]*models.Folder, error) {
	folders, err := models.ListFolders()
	if err != nil {
		return nil, err
	}
	manage := auth.Can(ctx, userID, perm.PermFlowsManage)
	var ranks map[uint]int
	if !manage {
		groupIDs, _ := models.GetUserGroupIDs(userID)
		if ranks, err = models.FolderRanks(userID, groupIDs); err != nil {
			return nil, err
		}
	}
	out := make([]*models.Folder, 0, len(folders))
	for _, f := range folders {
		rank := models.FlowAccessRank(models.FlowAccessAdmin)
		if !manage {
			rank = ranks[f.ID]
		}
		if rank < models.FlowAccessRank(models.FlowAccessView) {
			continue
		}
		f.AccessLevel = models.FlowAccessLevel(rank)
		// A parent the user cannot see is not named to them; the folder sits
		// at their root instead.
		if f.ParentID != nil && !manage && ranks[*f.ParentID] < models.FlowAccessRank(models.FlowAccessView) {
			f.ParentID = nil
		}
		out = append(out, f)
	}
	return out, nil
}

// MaskFolder clears a flow item's folder when the user may not see that
// folder, so a flow shared out of someone else's folder shows at the user's
// root without naming the folder.
func MaskFolder(ctx context.Context, userID uuid.UUID, item *FlowItem) {
	if item.FolderID == nil {
		return
	}
	if FolderRank(ctx, userID, *item.FolderID) < models.FlowAccessRank(models.FlowAccessView) {
		item.FolderID = nil
	}
}

// NameTaken reports whether a flow the user may read already uses the slug a
// name produces. A clash with a flow they may not read is not reported: the
// new flow takes a suffixed slug instead, so a refusal cannot confirm a
// private flow's name.
func NameTaken(ctx context.Context, userID uuid.UUID, name string) bool {
	var flows []*models.Flow
	if err := db.DB.Where("slug = ?", models.Slugify(name)).Find(&flows).Error; err != nil {
		return false
	}
	for _, f := range flows {
		if Holds(ctx, userID, f, models.FlowAccessView) {
			return true
		}
	}
	return false
}

// SlugMatch is one flow a slug could mean, as a chooser lists it.
type SlugMatch struct {
	ID    string `json:"id" description:"The flow's ID, its canonical address."`
	Name  string `json:"name" description:"Display name."`
	Owner string `json:"owner,omitempty" description:"The owner's username."`
}

// SlugMatches returns the flows using a slug that the user may read. Flows
// they may not read never appear, so a slug confirms nothing private.
func SlugMatches(ctx context.Context, userID uuid.UUID, slug string) ([]SlugMatch, error) {
	flows, err := models.FlowsWithSlug(slug)
	if err != nil {
		return nil, err
	}
	out := []SlugMatch{}
	for _, f := range flows {
		if Holds(ctx, userID, f, models.FlowAccessView) {
			out = append(out, SlugMatch{ID: f.ID.String(), Name: f.Name, Owner: f.Owner.Username})
		}
	}
	return out, nil
}
