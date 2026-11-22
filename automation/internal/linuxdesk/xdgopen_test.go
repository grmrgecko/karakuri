//go:build !windows && !darwin

package linuxdesk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultSchemeHandler pins the mimeapps.list shape a real desktop
// writes (captured from a KDE session's ~/.config/mimeapps.list): defaults
// live in [Default Applications] and not [Added Associations], values carry
// a trailing semicolon, and the answer must be an installed entry — a listed
// handler that is not on disk falls through to the next one.
func TestDefaultSchemeHandler(t *testing.T) {
	config := t.TempDir()
	data := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(config, "none"))
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", filepath.Join(data, "none"))
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")

	require.NoError(t, os.WriteFile(filepath.Join(config, "mimeapps.list"), []byte(
		"[Added Associations]\n"+
			"x-scheme-handler/https=added-only.desktop;\n"+
			"\n"+
			"[Default Applications]\n"+
			"x-scheme-handler/https=missing.desktop;zen.desktop;\n"+
			"x-scheme-handler/mailto=thunderbird.desktop\n"), 0o644))
	apps := filepath.Join(data, "applications")
	require.NoError(t, os.MkdirAll(apps, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(apps, "zen.desktop"),
		[]byte("[Desktop Entry]\nExec=zen %u\n"), 0o644))

	id, ok := defaultSchemeHandler("https")
	require.True(t, ok, "a default with an installed entry must resolve")
	assert.Equal(t, "zen", id, "the first installed entry wins, skipping the missing one")

	_, ok = defaultSchemeHandler("mailto")
	assert.False(t, ok, "a default whose entry is not installed must not resolve")

	_, ok = defaultSchemeHandler("ftp")
	assert.False(t, ok, "an association listed only under Added must not become the default")
}

// TestExpandExec pins how an entry's Exec line becomes an argv. Field codes
// are substituted wherever they appear in a word, which is how the desktop's
// own launcher reads them. It covers the regression where a code inside a
// larger argument reached the application as literal text and the URL being
// opened was dropped.
func TestExpandExec(t *testing.T) {
	const entry = "/usr/share/applications/app.desktop"
	const link = "https://example.org/a"
	for _, tc := range []struct {
		name string
		exec string
		args []string
		want []string
	}{
		{"a code that is its own word", `app %u`, []string{link}, []string{"app", link}},
		{"a code with nothing to substitute", `app %U`, nil, []string{"app"}},
		{"a code inside an option", `app --url=%u`, []string{link}, []string{"app", "--url=" + link}},
		{"a code inside a quoted command", `sh -c "exec app -url %U"`, []string{link}, []string{"sh", "-c", "exec app -url '" + link + "'"}},
		{"a value cannot extend a quoted command", `sh -c "exec app %u"`, []string{"x'; touch owned; '"}, []string{"sh", "-c", `exec app 'x'\''; touch owned; '\'''`}},
		{"the entry path and the unused codes", `app %i %c --entry=%k`, nil, []string{"app", "--entry=" + entry}},
		{"no code at all", `app --new-window`, []string{link}, []string{"app", "--new-window", link}},
		{"an escaped percent is not a code", `app --zoom=100%%`, []string{link}, []string{"app", "--zoom=100%", link}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandExec(tc.exec, entry, tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "the Exec line %q must expand to the argv the entry describes", tc.exec)
		})
	}
}

// TestParseDesktopEntryUnescapesExec locks the order the entry format gives an
// Exec value: the string escapes of the file are undone first and the quoting
// rules read the result. An entry writes a literal dollar sign in a quoted
// argument as \\$, and reading only the quoting layer left a backslash in
// front of it, so the shell printed the variable's name instead of its value.
func TestParseDesktopEntryUnescapesExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.desktop")
	for _, tc := range []struct {
		name string
		exec string
		want []string
	}{
		{"a dollar sign in a quoted argument", `sh -c "echo \\$HOME"`, []string{"sh", "-c", "echo $HOME"}},
		{"a backslash in a quoted argument", `app "C:\\\\dir"`, []string{"app", `C:\dir`}},
		{"an escaped space outside quotes", `app\s--flag`, []string{"app", "--flag"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, []byte("[Desktop Entry]\nExec="+tc.exec+"\n"), 0o644))
			entry, err := parseDesktopEntry(path)
			require.NoError(t, err)
			got, err := expandExec(entry.exec, path, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "the Exec value %q must be unescaped as a string before its quoting is read", tc.exec)
		})
	}
}
