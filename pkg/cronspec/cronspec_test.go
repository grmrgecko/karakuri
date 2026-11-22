package cronspec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDescribe pins the English the schedule node shows under a cron field.
// The strings come from this package rather than the cron library, so they are
// the editor's copy: an author reads them to confirm what they typed means
// what they meant, and a changed phrasing is a changed UI. The rows cover the
// shapes the parser treats differently — plain fields, lists, steps, textual
// month and weekday names, and the @descriptors — plus the two spellings of
// Sunday, which the standard allows as both 0 and 7.
func TestDescribe(t *testing.T) {
	cases := []struct {
		expr string
		want string
		why  string
	}{
		{expr: "* * * * *", want: "Every minute"},
		{expr: "*/5 * * * *", want: "Every 5 minutes"},
		{expr: "0 8 * * *", want: "At 08:00"},
		{expr: "0 8 * * 1-5", want: "At 08:00, on Monday through Friday"},
		{expr: "0 0 1 * *", want: "At 00:00, on day-of-month 1"},
		{expr: "30 4 1,15 * 5", want: "At 04:30, on day-of-month 1 and 15, on Friday"},
		{expr: "*/10 * * * * *", want: "Every 10 seconds", why: "a sixth field is seconds"},
		{expr: "0 */2 * * *", want: "At minute 0 past every 2 hours"},
		{expr: "15 9 * 3 *", want: "At 09:15, in March"},
		{expr: "5,10 8 * * *", want: "At minute 5 and 10 past hour 8"},
		{expr: "0 0 * JAN,JUL *", want: "At 00:00, in January and July", why: "month names are accepted"},
		{expr: "0 8 * * MON", want: "At 08:00, on Monday", why: "weekday names are accepted"},
		{expr: "0 12 * * 0", want: "At 12:00, on Sunday"},
		{expr: "0 12 * * 7", want: "At 12:00, on Sunday", why: "7 and 0 are both Sunday in the standard"},
		{expr: "@hourly", want: "At minute 0 past every hour"},
		{expr: "@daily", want: "At 00:00"},
		{expr: "@weekly", want: "At 00:00 on Sunday"},
		{expr: "*/15 9-17 * * MON-FRI", want: "At every 15th minute past hour 9 through 17, on Monday through Friday"},
		{expr: "0 0 */3 * *", want: "At 00:00, on every 3rd day-of-month", why: "step values are rendered as ordinals"},
		{expr: "*/2 * * * *", want: "Every 2 minutes"},
		{expr: "0 0 1 */2 *", want: "At 00:00, on day-of-month 1, in every 2nd month"},
		{expr: "not a cron", want: "", why: "unparseable input describes nothing rather than guessing"},
		{expr: "", want: "", why: "an empty field shows no description, not an error"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, Describe(c.expr), "Describe(%q) is what the editor prints: %s", c.expr, c.why)
	}
}

// TestValidate covers what the schedule node accepts at deploy. Both halves of
// a schedule can be wrong independently — the expression and the zone — and a
// zone the server cannot load has to be caught here rather than at the first
// missed trigger.
func TestValidate(t *testing.T) {
	require.NoError(t, Validate("0 8 * * *", "America/Chicago"),
		"a plain daily schedule in a real zone must be accepted")

	cases := []struct {
		name, expr, zone string
	}{
		{name: "an empty expression", expr: "", zone: ""},
		{name: "an unparseable expression", expr: "nonsense", zone: ""},
		{name: "a zone the server cannot load", expr: "0 8 * * *", zone: "Not/AZone"},
	}
	for _, c := range cases {
		assert.Errorf(t, Validate(c.expr, c.zone), "%s must be refused at deploy, not at the first missed trigger", c.name)
	}
}

// TestNextUsesConfiguredZone is the reason a schedule stores a zone at all.
// 08:00 in Chicago is 14:00 UTC on this winter date, so a schedule that
// resolved in the server's own zone would fire six hours off for anyone who
// did not host in their own timezone.
func TestNextUsesConfiguredZone(t *testing.T) {
	from := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	next, err := Next("0 8 * * *", "America/Chicago", from, 3)
	require.NoError(t, err, "a valid schedule must resolve its next occurrences")
	require.Len(t, next, 3, "three occurrences were requested")

	for _, at := range next {
		assert.Equalf(t, 8, at.Hour(), "%s must be 08:00 in the configured zone, not the server's", at)
	}
	assert.True(t, next[0].Before(next[1]) && next[1].Before(next[2]),
		"occurrences must be returned in ascending order: the editor lists them as a preview")
}

// TestFieldLabels covers the per-field captions the editor draws above a cron
// expression. Which labels apply depends on how many fields were typed, and a
// @descriptor has no fields to label at all.
func TestFieldLabels(t *testing.T) {
	five := FieldLabels("0 8 * * *")
	require.Len(t, five, 5, "a five-field expression is minute through weekday")
	assert.Equal(t, "minute", five[0], "the leading field of a five-field expression is the minute")

	six := FieldLabels("0 0 8 * * *")
	require.Len(t, six, 6, "a sixth field shifts everything along by one")
	assert.Equal(t, "second", six[0], "the leading field of a six-field expression is the second")

	assert.Nil(t, FieldLabels("@hourly"), "a descriptor has no fields, so there is nothing to caption")
}

// TestInlineZonePrefix covers a spec that names its zone the way the cron
// library reads it, as a leading CRON_TZ= or TZ= word. Validate accepts that
// spelling when no separate zone is set, so the caption, the field labels, and
// the preview must read the same schedule the parser runs. The description
// took the zone word for the seconds field ("At second CRON_TZ=... past minute
// 0 past hour 8"), and the preview rendered 08:00 Chicago in the server's zone.
func TestInlineZonePrefix(t *testing.T) {
	for _, expr := range []string{"CRON_TZ=America/Chicago 0 8 * * *", "TZ=America/Chicago 0 8 * * *"} {
		require.NoErrorf(t, Validate(expr, ""), "%q is a spec the parser accepts", expr)
		assert.Equalf(t, "At 08:00", Describe(expr), "the zone word of %q is not a cron field", expr)
		labels := FieldLabels(expr)
		require.Lenf(t, labels, 5, "%q has five cron fields after its zone word", expr)
		assert.Equalf(t, "minute", labels[0], "the first field of %q after the zone word is the minute", expr)

		next, err := Next(expr, "", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), 1)
		require.NoErrorf(t, err, "%q must resolve its next occurrence", expr)
		require.Len(t, next, 1)
		assert.Equalf(t, "America/Chicago", next[0].Location().String(), "%s must be shown in the zone the spec names", next[0])
		assert.Equalf(t, 8, next[0].Hour(), "%s must read 08:00 in that zone", next[0])
	}
	assert.Equal(t, "At minute 0 past every hour", Describe("CRON_TZ=UTC @hourly"),
		"a descriptor behind a zone word is still a descriptor")
}
