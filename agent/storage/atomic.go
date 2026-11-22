package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// stagingPath reserves a hidden sibling name without leaving a file behind.
// Keeping the stage on the destination filesystem lets the final rename take
// effect as one directory operation.
func stagingPath(destination, kind string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(destination), ".karakuri-"+kind+"-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

// commitReplacement installs a completed staging path without discarding the
// prior destination first. The backup dance is needed on Windows and for
// non-empty directories, where rename does not replace an existing path.
func commitReplacement(staged, destination string, overwrite bool) error {
	_, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return os.Rename(staged, destination)
	}
	if err != nil {
		return fmt.Errorf("cannot check %s: %w", destination, err)
	}
	if !overwrite {
		return fmt.Errorf("%s already exists; turn on replacing to overwrite it", destination)
	}

	backup, err := stagingPath(destination, "backup")
	if err != nil {
		return fmt.Errorf("reserving a backup for %s: %w", destination, err)
	}
	if err := os.Rename(destination, backup); err != nil {
		return fmt.Errorf("preserving %s before replacement: %w", destination, err)
	}
	if err := os.Rename(staged, destination); err != nil {
		if rollbackErr := os.Rename(backup, destination); rollbackErr != nil {
			return fmt.Errorf("installing %s: %w; restoring the prior destination: %v", destination, err, rollbackErr)
		}
		return fmt.Errorf("installing %s: %w", destination, err)
	}
	if err := removeTree(backup); err != nil {
		return fmt.Errorf("replaced %s but cannot remove backup %s: %w", destination, backup, err)
	}
	return nil
}

// removeTree removes a staging or backup path this package created. A copied
// tree keeps its source's directory modes, and the children of a read-only
// directory cannot be unlinked, so directories are made owner-writable when
// the first attempt fails. Links are not followed.
func removeTree(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(entry string, d fs.DirEntry, walkErr error) error {
		if walkErr == nil && d.IsDir() {
			_ = os.Chmod(entry, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// writeReplacement writes a complete sibling file and installs it only after
// the data is on disk. Existing permissions and ownership are retained when
// possible.
func writeReplacement(path string, data []byte, mode os.FileMode) error {
	return writeGuarded(context.Background(), path, data, mode, true, false, "")
}

func writeGuarded(ctx context.Context, path string, data []byte, mode os.FileMode, preserve, create bool, expected string) error {
	if err := checkWriteDigest(ctx, path, expected); err != nil {
		return err
	}
	// A rename over a symlink replaces the link and leaves the file it names
	// unchanged, so replacement stages beside the resolved file instead. A
	// link whose target is missing does not resolve and is replaced itself.
	if !create {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
	}
	// Replacement installs over whatever the path names, and its backup is
	// removed recursively, so a directory there would be deleted with
	// everything in it.
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", path)
		}
		// A rename over a pipe, socket, or device node leaves a regular file
		// under the name, and whoever holds the old one open hears nothing.
		if info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
			return fmt.Errorf("%s is a pipe, socket, or device, which a replacing write would turn into a regular file; use append mode to write into it", path)
		}
	}
	var prior os.FileInfo
	if preserve && !create {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
			prior = info
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".karakuri-write-*")
	if err != nil {
		return err
	}
	staged := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(staged)
		}
	}()
	// Ownership is set before the mode, since changing the owner can clear
	// mode bits.
	if prior != nil {
		keepOwner(f, prior)
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkWriteDigest(ctx, path, expected); err != nil {
		return err
	}
	if create {
		// Linking fails atomically when any destination already exists.
		if err := os.Link(staged, path); err != nil {
			return fmt.Errorf("exclusively creating %s: %w", path, err)
		}
		if err := os.Remove(staged); err != nil {
			return fmt.Errorf("created %s but cannot remove staging file: %w", path, err)
		}
	} else if err := commitReplacement(staged, path, true); err != nil {
		return err
	}
	ok = true
	return nil
}

func validateWriteDigest(expected string) error {
	if expected == "" {
		return nil
	}
	digest, err := hex.DecodeString(expected)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("expected SHA-256 must contain 64 hexadecimal digits")
	}
	return nil
}

func checkWriteDigest(ctx context.Context, path, expected string) error {
	if expected == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("checking previous file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("expected SHA-256 requires an existing regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return fmt.Errorf("file changed while checking expected SHA-256")
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Read(buffer)
		hash.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
		return fmt.Errorf("file SHA-256 does not match expected digest")
	}
	return nil
}
