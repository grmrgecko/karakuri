package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxEntryBytes bounds one archive entry, so a damaged archive cannot fill
// the disk. The largest real entry is the 26 MB Python runtime.
const maxEntryBytes = 512 << 20

// extract unpacks a release archive into dir. Release archives wrap their
// files in one top-level directory, which is dropped.
func extract(name string, data []byte, dir string) error {
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return extractTarGz(data, dir)
	case strings.HasSuffix(name, ".zip"):
		return extractZip(data, dir)
	}
	return fmt.Errorf("unknown archive format %q", name)
}

// target maps an archive path into dir, dropping its top-level directory and
// refusing any path that would leave dir.
func target(dir, name string) (string, bool, error) {
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", false, fmt.Errorf("archive entry %q leaves the archive", name)
	}
	_, rest, ok := strings.Cut(clean, "/")
	if !ok || rest == "" {
		return "", false, nil
	}
	if strings.HasPrefix(rest, "../") || rest == ".." || path.IsAbs(rest) {
		return "", false, fmt.Errorf("archive entry %q leaves the archive", name)
	}
	return filepath.Join(dir, filepath.FromSlash(rest)), true, nil
}

func extractTarGz(data []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		dst, ok, err := target(dir, h.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(dst, tr, os.FileMode(h.Mode)&0o777); err != nil {
				return err
			}
		}
	}
}

func extractZip(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		dst, ok, err := target(dir, f.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(dst, r, f.Mode()&0o777)
		_ = r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes one entry, creating its directory and capping its size.
func writeFile(dst string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxEntryBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxEntryBytes {
		err = fmt.Errorf("archive entry %s is larger than %d bytes", dst, maxEntryBytes)
	}
	return err
}
