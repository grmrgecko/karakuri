package http

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	nethttp "net/http"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"
)

// distFS is the frontend bundle, written into dist/ by the frontend build
// before the server compiles. The directory holds a tracked .gitkeep so the
// embed resolves in a checkout that has not built the frontend; such a binary
// answers the web interface with a message saying how to build it.
//
//go:embed all:dist
var distFS embed.FS

// frontendFile is one embedded file with the validator browsers revalidate it
// against. Embedded files carry no modification time, so the content hash is
// what lets a reload answer 304 instead of sending the bundle again.
type frontendFile struct {
	data []byte
	etag string
}

// embeddedFrontend is the bundle with dist/ stripped, the root the handler
// serves from.
func embeddedFrontend() fs.FS {
	sub, _ := fs.Sub(distFS, "dist")
	return sub
}

// frontendHandler serves the single-page app in bundle. A path naming a file
// serves it, a missing hashed asset is a 404, and any other path serves
// index.html so the client router handles it.
func frontendHandler(log *zap.Logger, bundle fs.FS) nethttp.Handler {
	files := loadFrontend(log, bundle)
	index, ok := files["index.html"]
	if !ok {
		log.Warn("web interface not embedded in this binary; build it with make")
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			nethttp.Error(w, "the web interface was not built into this binary; build the server with make", nethttp.StatusServiceUnavailable)
		})
	}

	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		file, found := files[name]
		switch {
		case found && strings.HasPrefix(name, "assets/"):
			// Vite names every asset by its content hash, so a name never
			// changes meaning and can be cached for good.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case found:
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(name, "assets/"):
			// A chunk from an older build answered with index.html would fail
			// as a script with a MIME error instead of a plain 404.
			nethttp.NotFound(w, r)
			return
		default:
			name, file = "index.html", index
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("ETag", file.etag)
		nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(file.data))
	})
}

// loadFrontend reads the bundle into memory once, keyed by path. Dot files are
// left out, which is what keeps .gitkeep off the wire.
func loadFrontend(log *zap.Logger, bundle fs.FS) map[string]frontendFile {
	files := map[string]frontendFile{}
	err := fs.WalkDir(bundle, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		data, err := fs.ReadFile(bundle, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[p] = frontendFile{data: data, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
		return nil
	})
	if err != nil {
		log.Error("reading embedded web interface", zap.Error(err))
	}
	return files
}
