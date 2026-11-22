package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Directories under the data directory that an archive carries, by the entry
// prefix they take in it. The flow SQLite databases and spool segments are
// saved by writing a temporary file and renaming it over the old one, so any
// file opened here is complete and needs no lock from its writer.
var dataParts = []string{"agents", "uploads", "sql", "history-spool"}

// skippedParts are regenerated or diagnostic, and never archived.
var skippedParts = []string{
	"rendered dashboard images (renders)",
	"assistant traces",
	"logs",
	"the script runtime cache (wasm-cache)",
	"external plugin executables, listed by name and version instead",
}

// reservedDirs are directories under the data directory a flow files root
// must not be or contain, since they are archived as their own parts or
// must never be archived at all.
var reservedDirs = []string{"agents", "uploads", "sql", "history-spool", "backups", "renders", "wasm-cache"}

// userParts are data parts whose file names come from users rather than from
// karakuri's own writers, so no name in them marks a temporary file.
var userParts = []string{"uploads"}

// temporaryName reports files a writer is still producing: sqlstore's
// .query-* saves, spool segments and agent identity files ending .tmp, and
// partial archives.
func temporaryName(name string) bool {
	return strings.HasPrefix(name, ".query-") || strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".partial")
}

// fileRoot is one flow files root as the archive stores it.
type fileRoot struct {
	dir    string
	prefix string
}

// splitFileRoots sorts the configured storage roots into those the archive
// carries (strictly inside the data directory and clear of its other parts)
// and the reasons the rest are left out.
func splitFileRoots(dataDir string, roots []string) ([]fileRoot, []string) {
	var keep []fileRoot
	var skipped []string
	data, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, []string{fmt.Sprintf("flow file roots: %v", err)}
	}
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("flow file root %s: %v", root, err))
			continue
		}
		rel, err := filepath.Rel(data, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			skipped = append(skipped, fmt.Sprintf("flow file root %s, which is outside the data directory", root))
			continue
		}
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		reserved := false
		for _, r := range reservedDirs {
			if top == r {
				reserved = true
			}
		}
		if reserved {
			skipped = append(skipped, fmt.Sprintf("flow file root %s, which overlaps karakuri's own %s directory", root, top))
			continue
		}
		keep = append(keep, fileRoot{dir: abs, prefix: "files/" + filepath.ToSlash(rel)})
	}

	// A root repeated or inside another would archive its files twice under
	// one name, and restore creates each file exclusively.
	var distinct []fileRoot
	for i, root := range keep {
		covered := false
		for j, other := range keep {
			if i == j {
				continue
			}
			if root.dir == other.dir {
				covered = covered || j < i
			} else if strings.HasPrefix(root.dir, other.dir+string(filepath.Separator)) {
				covered = true
			}
		}
		if covered {
			skipped = append(skipped, fmt.Sprintf("flow file root %s, which another root already covers", root.dir))
			continue
		}
		distinct = append(distinct, root)
	}
	return distinct, skipped
}

// writeTree adds every regular file under dir to the archive as
// prefix/<relative path>. A missing dir adds nothing. Files removed between
// listing and opening are skipped, as are symbolic links and anything else
// that is not a regular file. With skipTemporary set, temporary names are
// skipped too; uploads and flow files are named by users and pass false. Each file is read to the end
// and closed before the next, so a save renaming over it waits at most one
// read on Windows.
//
// dir itself may be a symbolic link, as when uploads are kept on another
// disk. The walk does not follow links, so it is resolved first; left as a
// link it would be skipped whole and the archive would silently lack the
// part.
func writeTree(a *archiveWriter, dir, prefix string, skipTemporary bool) error {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || (skipTemporary && temporaryName(d.Name())) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		return a.addFile(prefix+"/"+filepath.ToSlash(rel), f)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
