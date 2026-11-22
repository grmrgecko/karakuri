// Package backup writes an installation to one encrypted archive, checks
// archives, and restores them.
package backup

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

// Format is the archive layout version this build writes and the highest it
// reads.
const Format = 1

// MinPassphrase is the shortest passphrase an archive accepts.
const MinPassphrase = 12

// manifestName is the archive's last entry.
const manifestName = "manifest.json"

// scryptWorkFactor is age's default scrypt cost, about one second on a
// current machine. Tests lower it.
var scryptWorkFactor = 18

// ErrPassphrase reports an archive that would not open: a wrong passphrase or
// a damaged header look the same to age, by design.
var ErrPassphrase = errors.New("wrong passphrase or damaged archive")

// Part is one archive entry as the manifest records it.
type Part struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Plugin is an external plugin loaded when the backup was taken.
type Plugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Manifest describes an archive's contents. It is written last so it can
// carry the checksums computed while the parts streamed.
type Manifest struct {
	Format       int              `json:"format"`
	Version      string           `json:"version"`
	CreatedAt    time.Time        `json:"created_at"`
	Installation string           `json:"installation"`
	Database     string           `json:"database"`
	Counts       map[string]int64 `json:"counts"`
	Tables       map[string]int64 `json:"tables"`
	Parts        []Part           `json:"parts"`
	Plugins      []Plugin         `json:"plugins"`
	FileRoots    []string         `json:"file_roots"`
	Skipped      []string         `json:"skipped"`
}

// CheckPassphrase refuses a passphrase too short to protect an archive that
// holds every stored credential.
func CheckPassphrase(passphrase string) error {
	if len([]rune(passphrase)) < MinPassphrase {
		return fmt.Errorf("the passphrase must be at least %d characters", MinPassphrase)
	}
	return nil
}

// archiveWriter streams tar entries through zstd and age, hashing each one.
type archiveWriter struct {
	enc   io.WriteCloser
	zw    *zstd.Encoder
	tw    *tar.Writer
	parts []Part
}

// newArchiveWriter starts an archive on w encrypted to the passphrase.
func newArchiveWriter(w io.Writer, passphrase string) (*archiveWriter, error) {
	if err := CheckPassphrase(passphrase); err != nil {
		return nil, err
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	recipient.SetWorkFactor(scryptWorkFactor)
	enc, err := age.Encrypt(w, recipient)
	if err != nil {
		return nil, fmt.Errorf("starting encryption: %w", err)
	}
	zw, err := zstd.NewWriter(enc, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	return &archiveWriter{enc: enc, zw: zw, tw: tar.NewWriter(zw)}, nil
}

// add writes one entry of exactly size bytes read from r.
func (a *archiveWriter) add(name string, size int64, modTime time.Time, r io.Reader) error {
	// The same rule verify applies, so a name verify would refuse fails here
	// before the archive is written rather than after.
	if name == manifestName {
		return fmt.Errorf("archive entry name %q is not allowed", name)
	}
	if err := safeName(name); err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     size,
		ModTime:  modTime.UTC(),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}
	if err := a.tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(a.tw, h), io.LimitReader(r, size))
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if n != size {
		return fmt.Errorf("writing %s: %d of %d bytes were readable", name, n, size)
	}
	a.parts = append(a.parts, Part{Name: name, Size: size, SHA256: hex.EncodeToString(h.Sum(nil))})
	return nil
}

// addFile writes an open file as one entry. The size comes from the open
// handle, which keeps reading the version it opened even if a save renames a
// newer one over its path.
func (a *archiveWriter) addFile(name string, f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return a.add(name, info.Size(), info.ModTime(), f)
}

// addBytes writes an in-memory entry.
func (a *archiveWriter) addBytes(name string, data []byte, modTime time.Time) error {
	return a.add(name, int64(len(data)), modTime, bytes.NewReader(data))
}

// finish writes the manifest with the parts recorded so far and closes every
// layer. The archive is not authentic until finish succeeds: age writes its
// final chunk on close.
func (a *archiveWriter) finish(m *Manifest) error {
	m.Format = Format
	m.Parts = a.parts
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(data)), ModTime: m.CreatedAt.UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := a.tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := a.tw.Write(data); err != nil {
		return err
	}
	if err := a.tw.Close(); err != nil {
		return err
	}
	if err := a.zw.Close(); err != nil {
		return err
	}
	return a.enc.Close()
}

// abort releases the compressor without producing a valid archive.
func (a *archiveWriter) abort() {
	a.zw.Close()
}

// Entry is one archive part handed to a walk callback.
type Entry struct {
	Name string
	Size int64
	R    io.Reader
}

// walkArchive decrypts and reads an archive from r, calling fn for every part
// in order, then checks every part's size and checksum and the part list
// against the manifest. fn may read all, some, or none of an entry; the rest
// is drained so the checksum covers it. A walk that returns no error has read
// an authentic, complete archive.
func walkArchive(r io.Reader, passphrase string, fn func(Entry) error) (*Manifest, error) {
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, ErrPassphrase
	}
	identity.SetMaxWorkFactor(22)
	dec, err := age.Decrypt(r, identity)
	if err != nil {
		return nil, ErrPassphrase
	}
	zr, err := zstd.NewReader(dec)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	var parts []Part
	var manifest *Manifest
	last := "the archive header"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive is damaged after %s: %w", last, err)
		}
		if manifest != nil {
			return nil, fmt.Errorf("archive has %s after its manifest", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("archive entry %s is not a regular file", hdr.Name)
		}
		if hdr.Name == manifestName {
			data, err := io.ReadAll(io.LimitReader(tr, 16<<20))
			if err != nil {
				return nil, fmt.Errorf("archive is damaged in its manifest: %w", err)
			}
			manifest = &Manifest{}
			if err := json.Unmarshal(data, manifest); err != nil {
				return nil, fmt.Errorf("reading the manifest: %w", err)
			}
			continue
		}
		if err := safeName(hdr.Name); err != nil {
			return nil, err
		}
		h := sha256.New()
		counted := &countingReader{r: io.TeeReader(tr, h)}
		if fn != nil {
			if err := fn(Entry{Name: hdr.Name, Size: hdr.Size, R: counted}); err != nil {
				return nil, err
			}
		}
		if _, err := io.Copy(io.Discard, counted); err != nil {
			return nil, fmt.Errorf("archive is damaged in %s: %w", hdr.Name, err)
		}
		parts = append(parts, Part{Name: hdr.Name, Size: counted.n, SHA256: hex.EncodeToString(h.Sum(nil))})
		last = hdr.Name
	}
	if manifest == nil {
		return nil, fmt.Errorf("archive is incomplete: it has no manifest")
	}
	if manifest.Format > Format {
		return nil, fmt.Errorf("archive format %d is newer than this karakuri reads (%d); upgrade before using it", manifest.Format, Format)
	}
	if len(parts) != len(manifest.Parts) {
		return nil, fmt.Errorf("archive holds %d parts but its manifest lists %d", len(parts), len(manifest.Parts))
	}
	for i, p := range manifest.Parts {
		got := parts[i]
		if got.Name != p.Name {
			return nil, fmt.Errorf("archive part %d is %s but the manifest lists %s", i+1, got.Name, p.Name)
		}
		if got.Size != p.Size || got.SHA256 != p.SHA256 {
			return nil, fmt.Errorf("archive part %s does not match its checksum", p.Name)
		}
	}
	return manifest, nil
}

// safeName refuses entry names that would leave a staging directory.
func safeName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("archive entry name %q is not allowed", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return fmt.Errorf("archive entry name %q is not allowed", name)
		}
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
