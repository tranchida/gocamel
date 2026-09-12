package gocamel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TarGzDataFormat marshals and unmarshals gzipped tar archives. The body
// conventions match ZipDataFormat:
//
//   - Marshal: single entry ([]byte/string, named via TarGzEntryName or the
//     CamelFileName header) or multiple entries (map[string][]byte,
//     map[string]string, TarGzFiles, []TarGzFile).
//   - Unmarshal: a single regular file yields its raw content; multiple files
//     yield map[string][]byte.
type TarGzDataFormat struct {
	entryName string
	// flat stores only the base name of each entry.
	flat bool
	// gzipLevel is the compression level (gzip.DefaultCompression by default).
	gzipLevel    int
	maxEntrySize int64
	maxTotalSize int64
	maxEntries   int64
}

// TarGzFile is a named entry of a tar.gz archive.
type TarGzFile struct {
	Name    string
	Content []byte
	Mode    int64
	ModTime time.Time
}

// TarGzFiles is an ordered collection of tar.gz entries.
type TarGzFiles []TarGzFile

// TarGzOption configures a TarGzDataFormat.
type TarGzOption func(*TarGzDataFormat)

// TarGzEntryName sets the entry name used when marshalling a single
// []byte or string body (default "data"). A CamelFileName header value takes
// precedence over this option.
func TarGzEntryName(name string) TarGzOption {
	return func(f *TarGzDataFormat) {
		if name != "" {
			f.entryName = name
		}
	}
}

// TarGzFlat stores only the base name of each entry (directory structure is
// dropped).
func TarGzFlat(flat bool) TarGzOption {
	return func(f *TarGzDataFormat) { f.flat = flat }
}

// TarGzCompressionLevel sets the gzip compression level (1..9, or -1 for the
// default).
func TarGzCompressionLevel(level int) TarGzOption {
	return func(f *TarGzDataFormat) { f.gzipLevel = level }
}

// TarGzMaxEntrySize bounds the decompressed size of a single entry while
// unmarshalling (tar bomb protection). Non-positive values disable the check;
// the default is 512 MiB per entry.
func TarGzMaxEntrySize(max int64) TarGzOption {
	return func(f *TarGzDataFormat) { f.maxEntrySize = max }
}

// TarGzMaxTotalSize bounds the sum of decompressed entry sizes while
// unmarshalling. Non-positive values disable the check; the default is 1 GiB.
func TarGzMaxTotalSize(max int64) TarGzOption {
	return func(f *TarGzDataFormat) { f.maxTotalSize = max }
}

// TarGzMaxEntries bounds the number of regular-file entries while unmarshalling.
// Non-positive values disable the check; the default is 4096.
func TarGzMaxEntries(max int64) TarGzOption {
	return func(f *TarGzDataFormat) { f.maxEntries = max }
}

// NewTarGzDataFormat creates a tar.gz data format with the provided options.
func NewTarGzDataFormat(opts ...TarGzOption) *TarGzDataFormat {
	format := &TarGzDataFormat{
		entryName:    "data",
		gzipLevel:    gzip.DefaultCompression,
		maxEntrySize: 512 << 20,
		maxTotalSize: 1 << 30,
		maxEntries:   4096,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(format)
		}
	}
	return format
}

// Marshal builds a gzipped tar archive from the body.
func (f *TarGzDataFormat) Marshal(exchange *Exchange, body any) (any, error) {
	files, err := f.filesForMarshal(exchange, body)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("tar.gz marshal: nothing to archive")
	}

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, f.gzipLevel)
	if err != nil {
		return nil, fmt.Errorf("tar.gz marshal: %w", err)
	}
	tw := tar.NewWriter(zw)

	now := time.Now()
	for _, file := range files {
		name := file.Name
		if f.flat {
			name = path.Base(name)
		}
		if err := validateTarEntryName(name); err != nil {
			return nil, err
		}
		modTime := file.ModTime
		if modTime.IsZero() {
			modTime = now
		}
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Name:    name,
			Mode:    mode,
			Size:    int64(len(file.Content)),
			ModTime: modTime,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("tar.gz marshal: writing header for %q: %w", name, err)
		}
		if _, err := tw.Write(file.Content); err != nil {
			return nil, fmt.Errorf("tar.gz marshal: writing entry %q: %w", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar.gz marshal: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("tar.gz marshal: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal extracts entries from a gzipped tar archive.
func (f *TarGzDataFormat) Unmarshal(exchange *Exchange, body any) (any, error) {
	data, err := bodyToBytes(body)
	if err != nil {
		return nil, fmt.Errorf("tar.gz unmarshal: expects gzipped tar bytes, string or io.Reader: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("tar.gz unmarshal: empty archive")
	}

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("tar.gz unmarshal: %w", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	contents := make(map[string][]byte)
	var names []string

	limit := f.maxEntrySize
	if limit <= 0 {
		limit = 1 << 62
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar.gz unmarshal: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // skip directories, symlinks and special files
		}
		name, err := validateTarEntryReadName(hdr.Name)
		if err != nil {
			return nil, err
		}
		if hdr.Size > limit {
			return nil, fmt.Errorf("tar.gz unmarshal: entry %q exceeds the maximum size of %d bytes (possible tar bomb)", name, limit)
		}
		content, err := io.ReadAll(io.LimitReader(tr, limit+1))
		if err != nil {
			return nil, fmt.Errorf("tar.gz unmarshal: reading entry %q: %w", name, err)
		}
		if int64(len(content)) > limit {
			return nil, fmt.Errorf("tar.gz unmarshal: entry %q exceeds the maximum size of %d bytes (possible tar bomb)", name, limit)
		}
		contents[name] = content
		names = append(names, name)
	}

	if exchange != nil {
		exchange.GetIn().SetHeader(CamelZipEntryCount, len(names))
		exchange.GetIn().SetHeader(CamelZipEntryNames, names)
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("tar.gz unmarshal: archive contains no regular file")
	}
	if len(names) == 1 {
		return contents[names[0]], nil
	}
	return contents, nil
}

func (f *TarGzDataFormat) filesForMarshal(exchange *Exchange, body any) (TarGzFiles, error) {
	switch v := body.(type) {
	case nil:
		return nil, fmt.Errorf("tar.gz marshal: nil body")
	case string:
		return TarGzFiles{{Name: f.nameFor(exchange), Content: []byte(v)}}, nil
	case []byte:
		return TarGzFiles{{Name: f.nameFor(exchange), Content: v}}, nil
	case TarGzFile:
		return TarGzFiles{v}, nil
	case TarGzFiles:
		return v, nil
	case []TarGzFile:
		return v, nil
	case map[string]string:
		files := make(TarGzFiles, 0, len(v))
		for name, content := range v {
			files = append(files, TarGzFile{Name: name, Content: []byte(content)})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		return files, nil
	case map[string][]byte:
		files := make(TarGzFiles, 0, len(v))
		for name, content := range v {
			files = append(files, TarGzFile{Name: name, Content: content})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		return files, nil
	default:
		return nil, fmt.Errorf("tar.gz marshal: unsupported body type %T (want []byte, string, TarGzFile(s) or map[string][]byte)", body)
	}
}

func (f *TarGzDataFormat) nameFor(exchange *Exchange) string {
	if exchange != nil {
		if name, ok := exchange.GetIn().GetHeader(CamelFileName); ok {
			if s, ok := name.(string); ok && s != "" {
				return s
			}
		}
	}
	if f.entryName != "" {
		return f.entryName
	}
	return "data"
}

// validateTarEntryName rejects entry names that could escape the extraction
// root (tar-slip) or that contain null bytes.
func validateTarEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("tar.gz marshal: empty entry name")
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("tar.gz marshal: entry name %q contains a null byte", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("tar.gz marshal: entry name %q escapes the archive root", name)
	}
	return nil
}

// validateTarEntryReadName validates an entry name read from an archive,
// rejecting traversal and null bytes (tar-slip protection) instead of
// renaming entries silently.
func validateTarEntryReadName(name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("tar.gz unmarshal: entry name %q contains a null byte", name)
	}
	if name == "" {
		return "", fmt.Errorf("tar.gz unmarshal: empty entry name")
	}
	if containsTraversal(name) {
		return "", fmt.Errorf("tar.gz unmarshal: entry name %q escapes the archive root", name)
	}
	clean := path.Clean(name)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("tar.gz unmarshal: invalid entry name %q", name)
	}
	return clean, nil
}

// MarshalZip adds a ZIP marshal step to the route.
func (b *RouteBuilder) MarshalZip(opts ...ZipOption) *RouteBuilder {
	return b.Marshal(NewZipDataFormat(opts...))
}

// UnmarshalZip adds a ZIP unmarshal step to the route.
func (b *RouteBuilder) UnmarshalZip(opts ...ZipOption) *RouteBuilder {
	return b.Unmarshal(NewZipDataFormat(opts...))
}

// MarshalTarGz adds a tar.gz marshal step to the route.
func (b *RouteBuilder) MarshalTarGz(opts ...TarGzOption) *RouteBuilder {
	return b.Marshal(NewTarGzDataFormat(opts...))
}

// UnmarshalTarGz adds a tar.gz unmarshal step to the route.
func (b *RouteBuilder) UnmarshalTarGz(opts ...TarGzOption) *RouteBuilder {
	return b.Unmarshal(NewTarGzDataFormat(opts...))
}
