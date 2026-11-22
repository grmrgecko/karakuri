//go:build !windows && !darwin

package linuxdesk

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// A .desktop id is launched by reading the entry's own Exec line rather than
// through gtk-launch: the file format is a spec, the lookup is three
// directories, and doing it here means launching an application needs nothing
// installed. Entries this parser will not run — Terminal=true, or an Exec that
// cannot be read — fall back to gtk-launch, which knows how to put a terminal
// application in a terminal.

// errNeedsTerminal marks an entry that declares Terminal=true, which running
// detached would start invisibly and uselessly.
var errNeedsTerminal = errors.New("the entry wants a terminal")

// findDesktopEntry locates <id>.desktop in the XDG application directories,
// user tier first, the same order the desktop's own launcher searches.
func findDesktopEntry(id string) (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}

	dirs := []string{}
	if dataHome != "" {
		dirs = append(dirs, dataHome)
	}
	dirs = append(dirs, filepath.SplitList(dataDirs)...)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "applications", id+".desktop")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no application called %s is installed", id)
}

// launchDesktopEntry starts the application a .desktop id names, detached.
func launchDesktopEntry(id string, args []string) error {
	path, err := findDesktopEntry(id)
	if err != nil {
		return err
	}
	entry, err := parseDesktopEntry(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	if entry.terminal {
		return errNeedsTerminal
	}
	argv, err := expandExec(entry.exec, path, args)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	if len(argv) == 0 {
		return fmt.Errorf("%s has an empty Exec line", filepath.Base(path))
	}
	return toolsh.StartDetached(argv[0], argv[1:]...)
}

// desktopEntry is the two keys a launch needs.
type desktopEntry struct {
	exec     string
	terminal bool
}

// parseDesktopEntry reads the [Desktop Entry] group's Exec and Terminal keys.
func parseDesktopEntry(path string) (desktopEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return desktopEntry{}, err
	}
	defer file.Close()

	var entry desktopEntry
	inGroup := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inGroup = line == "[Desktop Entry]"
			continue
		}
		if !inGroup {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Exec":
			entry.exec = unescapeEntryString(strings.TrimSpace(value))
		case "Terminal":
			entry.terminal = strings.TrimSpace(value) == "true"
		}
	}
	if err := scanner.Err(); err != nil {
		return desktopEntry{}, err
	}
	if entry.exec == "" {
		return desktopEntry{}, errors.New("the entry has no Exec line")
	}
	return entry, nil
}

// unescapeEntryString undoes the escapes the entry format defines for every
// string value: \s, \n, \t, \r, and \\. They are applied before the Exec
// quoting rules, which is why an entry writes a quoted backslash as four
// backslashes and a quoted dollar sign as \\$. An unknown escape is kept as
// written.
func unescapeEntryString(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var out strings.Builder
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '\\' || i+1 == len(runes) {
			out.WriteRune(runes[i])
			continue
		}
		i++
		switch runes[i] {
		case 's':
			out.WriteByte(' ')
		case 'n':
			out.WriteByte('\n')
		case 't':
			out.WriteByte('\t')
		case 'r':
			out.WriteByte('\r')
		case '\\':
			out.WriteByte('\\')
		default:
			out.WriteByte('\\')
			out.WriteRune(runes[i])
		}
	}
	return out.String()
}

// expandExec splits an Exec value into argv per the desktop entry spec —
// double-quoted arguments with backslash escapes — and substitutes the field
// codes: %f and %u take the first argument, %F and %U all of them, %k the
// entry's own path, and the rest expand to nothing. A code that is a whole word
// becomes one argument per value; a code inside a larger word is substituted in
// place, the way the desktop's own launcher reads an Exec line. Inside a quoted
// word the values are shell-quoted: such a word is nearly always a command line
// handed to a shell, and a URL must not be able to add commands to it.
func expandExec(exec, entryPath string, args []string) ([]string, error) {
	words, quoted, err := splitExec(exec)
	if err != nil {
		return nil, err
	}
	var argv []string
	takesArgs := false
	for i, word := range words {
		switch word {
		case "%f", "%u":
			takesArgs = true
			if len(args) > 0 {
				argv = append(argv, args[0])
			}
		case "%F", "%U":
			takesArgs = true
			argv = append(argv, args...)
		case "%k":
			argv = append(argv, entryPath)
		case "%i", "%c", "%d", "%D", "%n", "%N", "%v", "%m":
			// Icon, name, and the deprecated codes; nothing to substitute.
		default:
			expanded, used := expandWord(word, entryPath, args, quoted[i])
			takesArgs = takesArgs || used
			argv = append(argv, expanded)
		}
	}
	// An Exec with no file or URL code still receives the arguments, appended
	// the way gtk-launch passes extras.
	if len(args) > 0 && !takesArgs {
		argv = append(argv, args...)
	}
	return argv, nil
}

// expandWord substitutes the field codes inside one word and reports whether
// any of them took the launch arguments. shell selects shell-quoted values.
func expandWord(word, entryPath string, args []string, shell bool) (string, bool) {
	if !strings.Contains(word, "%") {
		return word, false
	}
	value := func(s string) string {
		if !shell {
			return s
		}
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	var out strings.Builder
	used := false
	runes := []rune(word)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '%' || i+1 == len(runes) {
			out.WriteRune(runes[i])
			continue
		}
		i++
		switch runes[i] {
		case '%':
			out.WriteByte('%')
		case 'f', 'u':
			used = true
			if len(args) > 0 {
				out.WriteString(value(args[0]))
			}
		case 'F', 'U':
			used = true
			for n, arg := range args {
				if n > 0 {
					out.WriteByte(' ')
				}
				out.WriteString(value(arg))
			}
		case 'k':
			out.WriteString(value(entryPath))
		case 'i', 'c', 'd', 'D', 'n', 'N', 'v', 'm':
		default:
			out.WriteByte('%')
			out.WriteRune(runes[i])
		}
	}
	return out.String(), used
}

// splitExec tokenizes an Exec line: space-separated words, double quotes
// grouping, backslash escaping inside quotes. quoted marks the words that held
// a quoted section.
func splitExec(exec string) (words []string, quoted []bool, err error) {
	var current strings.Builder
	inWord, inQuote, escaped, wordQuoted := false, false, false, false
	for _, r := range exec {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case inQuote && r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
			inWord, wordQuoted = true, true
		case !inQuote && (r == ' ' || r == '\t'):
			if inWord {
				words, quoted = append(words, current.String()), append(quoted, wordQuoted)
				current.Reset()
				inWord, wordQuoted = false, false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if inQuote {
		return nil, nil, errors.New("the Exec line has an unclosed quote")
	}
	if inWord {
		words, quoted = append(words, current.String()), append(quoted, wordQuoted)
	}
	return words, quoted, nil
}
