package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Archive and compression formats. The format is always declared rather than
// sniffed from the bytes, the same rule every other decoder here follows.
const (
	formatGzip  = "gzip"
	formatZip   = "zip"
	formatTar   = "tar"
	formatTarGz = "targz"
)

// maxArchiveBytes caps what one entry may expand to. A zip bomb is a small file
// that decompresses to a large one, and a flow engine that reads an archive off
// the internet has no other defense: the size in the header is written by
// whoever built the archive, so only counting bytes as they arrive is honest.
const maxArchiveBytes = 256 << 20

// archiveFormatField is the format selector both nodes share.
func archiveFormatField(help string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:      "format",
		Label:   "Format",
		Type:    flow.FieldSelect,
		Default: formatGzip,
		Options: []engine.Option{
			{Value: formatGzip, Label: "gzip (one value)"},
			{Value: formatZip, Label: "zip (an archive of files)"},
			{Value: formatTar, Label: "tar (an archive of files)"},
			{Value: formatTarGz, Label: "tar.gz (an archive of files)"},
		},
		Help: help,
	}
}

// isArchive reports whether a format holds named files rather than one value.
func isArchive(format string) bool {
	return format == formatZip || format == formatTar || format == formatTarGz
}

// DecompressNode expands gzip data or unpacks an archive.
//
// The node names are decompress and compress rather than following the
// decoder/<name>-encode spelling the other formats use, because the format here
// is a config field rather than the node's identity: one node reading zip and
// gzip is still one direction, which is what that convention is about.
type DecompressNode struct{}

func (DecompressNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "decompress",
		Label:       "Decompress",
		Folder:      flow.FolderParsers,
		Description: "Expands gzip data or unpacks a zip or tar archive",
		Icon:        "las la-file-archive",
		Inputs:      converterInput(flow.PortBinary),
		// gzip gives back one value, so the port carries bytes. An archive
		// gives back files, which is an object per file or an array of them.
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortBinary, TypeFrom: "in",
			TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: map[string][]string{"format": {formatZip, formatTar, formatTarGz}}, Type: flow.PortObject},
				},
				Default:     flow.PortBinary,
				PayloadPath: []string{"target", "property"},
			},
		}},
		Pure: true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			archiveFormatField("What the data is. Nothing is guessed from the bytes: an archive that is not the declared format fails."),
			{
				ID:      "output",
				Label:   "Unpack into",
				Type:    flow.FieldSelect,
				Default: "array",
				Options: []engine.Option{
					{Value: "array", Label: "One message holding every file"},
					{Value: "messages", Label: "A message per file"},
				},
				ShowIf: &flow.ShowIf{Field: "format", Equals: []string{formatZip, formatTar, formatTarGz}},
				Help: "A message per file fans out like a split, so a join downstream can put them " +
					"back together. Either way a file is an object with name, size, modified, and data.",
			},
			{
				ID:          "match",
				Label:       "Only files matching",
				Type:        flow.FieldString,
				Placeholder: "*.csv",
				ShowIf:      &flow.ShowIf{Field: "format", Equals: []string{formatZip, formatTar, formatTarGz}},
				Help:        "Shell-style glob against each entry's name. Empty takes every file.",
			},
		},
	}
}

// decompressConfig is the decoded decompress node configuration.
type decompressConfig struct {
	Property string `json:"property"`
	Target   string `json:"target"`
	Format   string `json:"format"`
	Output   string `json:"output"`
	Match    string `json:"match"`
}

// Execute expands the source value.
func (DecompressNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg decompressConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	data, err := archiveBytes(value, property)
	if err != nil {
		return nil, err
	}

	// gzip holds one value, so it is done here.
	if !isArchive(cfg.Format) {
		if cfg.Format != "" && cfg.Format != formatGzip {
			return nil, fmt.Errorf("unknown format %q", cfg.Format)
		}
		plain, err := gunzip(data)
		if err != nil {
			return nil, err
		}
		out, err := parserResult(msg, property, cfg.Target, plain)
		if err != nil {
			return nil, err
		}
		return engine.One("out", out), nil
	}

	files, err := unpack(data, cfg.Format, cfg.Match)
	if err != nil {
		return nil, err
	}
	if cfg.Output == "messages" {
		return parserFanOut(msg, property, cfg.Target, files)
	}
	out, err := parserResult(msg, property, cfg.Target, files)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// CompressNode gzips a value or packs files into an archive.
type CompressNode struct{}

func (CompressNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "compress",
		Label:       "Compress",
		Folder:      flow.FolderEncoders,
		Description: "Gzips a value or packs files into a zip or tar archive",
		Icon:        "las la-file-archive",
		// Packing reads files, gzip reads one value, so the input is only
		// pinned in the archive case.
		Inputs: []engine.PortSpec{{
			ID: "in", Label: "In", Type: flow.PortAny,
			TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: map[string][]string{"format": {formatZip, formatTar, formatTarGz}}, Type: flow.PortObject},
				},
				Default:     flow.PortAny,
				PayloadPath: []string{"property"},
			},
		}},
		Outputs: converterOutput(flow.PortBinary),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			archiveFormatField("gzip compresses the value as it stands. The archive formats read a list of files."),
			{
				ID:          "filename",
				Label:       "Name inside the archive",
				Type:        flow.FieldString,
				Placeholder: "readings.csv",
				ShowIf:      &flow.ShowIf{Field: "format", Equals: []string{formatZip, formatTar, formatTarGz}},
				Help: "Used when the value is one blob rather than a list of files. A list is an " +
					"array of objects with name and data, or an object of name to content.",
			},
			{
				ID:      "level",
				Label:   "Compression",
				Type:    flow.FieldSelect,
				Default: "default",
				Options: []engine.Option{
					{Value: "default", Label: "Default"},
					{Value: "best", Label: "Smallest"},
					{Value: "fast", Label: "Fastest"},
					{Value: "none", Label: "Store without compressing"},
				},
				ShowIf: &flow.ShowIf{Field: "format", Equals: []string{formatGzip, formatZip, formatTarGz}},
			},
		},
	}
}

// compressConfig is the decoded compress node configuration.
type compressConfig struct {
	Property string `json:"property"`
	Target   string `json:"target"`
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Level    string `json:"level"`
}

// Execute compresses the source value.
func (CompressNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg compressConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	level, err := compressionLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	var packed []byte
	if isArchive(cfg.Format) {
		files, err := archiveEntries(value, cfg.Filename, property)
		if err != nil {
			return nil, err
		}
		packed, err = pack(files, cfg.Format, level)
		if err != nil {
			return nil, err
		}
	} else {
		if cfg.Format != "" && cfg.Format != formatGzip {
			return nil, fmt.Errorf("unknown format %q", cfg.Format)
		}
		raw, err := archiveBytes(value, property)
		if err != nil {
			return nil, err
		}
		packed, err = gzipBytes(raw, level)
		if err != nil {
			return nil, err
		}
	}

	out, err := parserResult(msg, property, cfg.Target, packed)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// archiveBytes reads a value as the bytes to compress or expand. Text is used
// as its bytes so a JSON document or a log can be gzipped without a base64 step
// in front.
func archiveBytes(value any, property string) ([]byte, error) {
	switch t := value.(type) {
	case []byte:
		return t, nil
	case string:
		return []byte(t), nil
	default:
		return nil, fmt.Errorf("property %q is %T, not bytes or text", property, value)
	}
}

// archiveFile is one entry of an archive as a flow sees it.
type archiveFile struct {
	Name     string    `json:"name"`
	Size     int       `json:"size"`
	Modified time.Time `json:"modified"`
	Data     []byte    `json:"data"`
}

// compressionLevel maps the config choice to a flate level.
func compressionLevel(name string) (int, error) {
	switch name {
	case "", "default":
		return gzip.DefaultCompression, nil
	case "best":
		return gzip.BestCompression, nil
	case "fast":
		return gzip.BestSpeed, nil
	case "none":
		return gzip.NoCompression, nil
	default:
		return 0, fmt.Errorf("unknown compression %q", name)
	}
}

// gunzip expands a gzip stream, refusing one that expands past the cap.
func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading gzip data: %w", err)
	}
	defer zr.Close()
	return readCapped(zr, "gzip data")
}

// gzipBytes compresses a value.
func gzipBytes(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readCapped copies a decompressed stream, failing past maxArchiveBytes rather
// than growing until the process dies.
func readCapped(r io.Reader, what string) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", what, err)
	}
	if n > maxArchiveBytes {
		return nil, fmt.Errorf("%s expands past the %d byte limit", what, maxArchiveBytes)
	}
	return buf.Bytes(), nil
}

// unpack reads every file of an archive, in name order so a run is repeatable.
func unpack(data []byte, format, match string) ([]any, error) {
	var files []archiveFile
	var err error
	switch format {
	case formatZip:
		files, err = unpackZip(data)
	case formatTar:
		files, err = unpackTar(bytes.NewReader(data))
	case formatTarGz:
		zr, gzErr := gzip.NewReader(bytes.NewReader(data))
		if gzErr != nil {
			return nil, fmt.Errorf("reading tar.gz data: %w", gzErr)
		}
		defer zr.Close()
		files, err = unpackTar(zr)
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	out := make([]any, 0, len(files))
	for _, f := range files {
		keep, err := matchArchiveName(match, f.Name)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		out = append(out, map[string]any{
			"name":     f.Name,
			"size":     float64(f.Size),
			"modified": f.Modified.Format(time.RFC3339),
			"data":     f.Data,
		})
	}
	return out, nil
}

// matchArchiveName applies the glob filter, treating a pattern with no
// directory separator as matching the base name so "*.csv" finds a file
// nested in a folder.
func matchArchiveName(pattern, name string) (bool, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true, nil
	}
	target := name
	if !strings.Contains(pattern, "/") {
		target = path.Base(name)
	}
	ok, err := path.Match(pattern, target)
	if err != nil {
		return false, fmt.Errorf("invalid file pattern %q: %w", pattern, err)
	}
	return ok, nil
}

// unpackZip reads the entries of a zip archive.
func unpackZip(data []byte) ([]archiveFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("reading zip archive: %w", err)
	}
	var out []archiveFile
	total := 0
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || unsafeArchivePath(entry.Name) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("reading %q from the zip archive: %w", entry.Name, err)
		}
		body, err := readCapped(rc, fmt.Sprintf("%q", entry.Name))
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += len(body)
		if total > maxArchiveBytes {
			return nil, fmt.Errorf("the archive expands past the %d byte limit", maxArchiveBytes)
		}
		out = append(out, archiveFile{
			Name: entry.Name, Size: len(body), Modified: entry.Modified, Data: body,
		})
	}
	return out, nil
}

// unpackTar reads the entries of a tar stream.
func unpackTar(r io.Reader) ([]archiveFile, error) {
	tr := tar.NewReader(r)
	var out []archiveFile
	total := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || unsafeArchivePath(hdr.Name) {
			continue
		}
		body, err := readCapped(tr, fmt.Sprintf("%q", hdr.Name))
		if err != nil {
			return nil, err
		}
		total += len(body)
		if total > maxArchiveBytes {
			return nil, fmt.Errorf("the archive expands past the %d byte limit", maxArchiveBytes)
		}
		out = append(out, archiveFile{
			Name: hdr.Name, Size: len(body), Modified: hdr.ModTime, Data: body,
		})
	}
	return out, nil
}

// unsafeArchivePath reports an entry name that escapes the archive. Nothing
// here writes to disk, so this is not a path traversal fix; it drops the entry
// so a name a flow passes straight to file-write cannot walk out of
// storage.roots on the way.
func unsafeArchivePath(name string) bool {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || path.IsAbs(name) || path.Clean(name) == "." {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// archiveEntries reads what to pack: an array of {name, data} objects, an
// object of name to content, or one blob to store under the configured name.
func archiveEntries(value any, filename, property string) ([]archiveFile, error) {
	switch t := value.(type) {
	case []any:
		out := make([]archiveFile, 0, len(t))
		for i, item := range t {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("file %d is %T, not an object with name and data", i, item)
			}
			name := stringify(obj["name"])
			if name == "" {
				return nil, fmt.Errorf("file %d has no name", i)
			}
			body, err := archiveBytes(obj["data"], "data")
			if err != nil {
				return nil, fmt.Errorf("file %q: %w", name, err)
			}
			out = append(out, archiveFile{Name: name, Data: body})
		}
		return out, nil

	case map[string]any:
		names := make([]string, 0, len(t))
		for name := range t {
			names = append(names, name)
		}
		// Sorted so the same input packs to the same archive every time.
		sort.Strings(names)
		out := make([]archiveFile, 0, len(names))
		for _, name := range names {
			body, err := archiveBytes(t[name], name)
			if err != nil {
				return nil, err
			}
			out = append(out, archiveFile{Name: name, Data: body})
		}
		return out, nil

	default:
		body, err := archiveBytes(value, property)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(filename)
		if name == "" {
			return nil, errors.New("packing one value needs a name to store it under")
		}
		return []archiveFile{{Name: name, Data: body}}, nil
	}
}

// pack writes the files into an archive.
func pack(files []archiveFile, format string, level int) ([]byte, error) {
	switch format {
	case formatZip:
		return packZip(files, level)
	case formatTar:
		var buf bytes.Buffer
		if err := packTar(&buf, files); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case formatTarGz:
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, level)
		if err != nil {
			return nil, err
		}
		if err := packTar(zw, files); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
}

// packZip writes a zip archive.
func packZip(files []archiveFile, level int) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// The writer deflates at its own default level unless given a compressor,
	// which would leave the configured level without effect.
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, level)
	})
	method := zip.Deflate
	if level == gzip.NoCompression {
		method = zip.Store
	}
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: method})
		if err != nil {
			return nil, fmt.Errorf("adding %q to the zip archive: %w", f.Name, err)
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, fmt.Errorf("writing %q: %w", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// packTar writes a tar stream.
func packTar(w io.Writer, files []archiveFile) error {
	tw := tar.NewWriter(w)
	for _, f := range files {
		hdr := &tar.Header{
			Name: f.Name, Mode: 0o644, Size: int64(len(f.Data)), Typeflag: tar.TypeReg,
		}
		if !f.Modified.IsZero() {
			hdr.ModTime = f.Modified
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("adding %q to the tar archive: %w", f.Name, err)
		}
		if _, err := tw.Write(f.Data); err != nil {
			return fmt.Errorf("writing %q: %w", f.Name, err)
		}
	}
	return tw.Close()
}
