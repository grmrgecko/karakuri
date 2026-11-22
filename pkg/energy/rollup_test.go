package energy

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
)

// TestMembershipStartsInTheLocalHourOfJoining locks that a source counts
// toward a group from the local hour it joined in, in every zone. The join
// time was truncated on the UTC grid, so in a half-hour zone a source joined
// in the second half of a local hour (15:45 in Kolkata is 10:15Z, which
// truncates to 10:00Z, after the 09:30Z hour start) was left out of that
// hour's group total. A whole-hour zone always counted it. The hour before
// the join stays out of the group.
func TestMembershipStartsInTheLocalHourOfJoining(t *testing.T) {
	group, source := uuid.New(), uuid.New()
	tests := []struct {
		name   string
		loc    *time.Location
		joined time.Time
	}{
		{"whole-hour zone", chicago, time.Date(2026, 11, 2, 10, 15, 0, 0, time.UTC)},
		{"half-hour zone", mustLoc("Asia/Kolkata"), time.Date(2026, 11, 2, 10, 15, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &data{loc: tt.loc, members: []*models.EnergyGroupMember{{GroupID: group, SourceID: source, JoinedAt: tt.joined}}}
			hour := HourStart(tt.joined, tt.loc)
			assert.True(t, d.member(group, source, hour), "the hour holding the join counts toward the group")
			assert.False(t, d.member(group, source, hour.Add(-time.Hour)), "the hour before the join keeps its old grouping")
		})
	}
}
