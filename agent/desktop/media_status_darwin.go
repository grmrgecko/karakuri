//go:build darwin

package desktop

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strconv"
	"strings"
)

func readMediaStatus(ctx context.Context, player string) (map[string]any, error) {
	if player == "" {
		player = "Music"
	}
	if !strings.EqualFold(player, "Music") && !strings.EqualFold(player, "Spotify") {
		return nil, fmt.Errorf("macOS media status supports Music or Spotify, not %q", player)
	}
	script := fmt.Sprintf(`tell application %q
if not running then error "player is not running"
set t to current track
return (player state as text) & linefeed & (name of t as text) & linefeed & (artist of t as text) & linefeed & (album of t as text) & linefeed & (player position as text) & linefeed & (duration of t as text)
end tell`, player)
	out, err := osexec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 6 {
		return nil, fmt.Errorf("%s returned incomplete track details", player)
	}
	position, _ := strconv.ParseFloat(lines[4], 64)
	duration, _ := strconv.ParseFloat(lines[5], 64)
	return map[string]any{"player": player, "state": strings.ToLower(lines[0]),
		"playing": strings.EqualFold(lines[0], "playing"), "title": lines[1], "artist": lines[2],
		"album": lines[3], "position_seconds": position, "duration_seconds": duration}, nil
}
