package gocamel

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Header set on the exchange while unmarshalling multi-entry archives.
const (
	// CamelZipEntryCount stores the number of readable entries in the archive.
	CamelZipEntryCount = "CamelZipEntryCount"
	// CamelZipEntryNames stores the sorted entry names, as []string.
	CamelZipEntryNames = "CamelZipEntryNames"
)

// ZipDataFormat marshals and unmarshals ZIP archives.
//
// Marshal accepts:
//   - []byte or string: a single entry stored under EntryName (default "data")
//   - map[string][]byte, map[string]string or ZipFiles: multiple named entries
//   - []ZipFile: multiple named entries preserving order
//
// Unmarshal produces:
//   - a single entry: its raw content ([]byte)
//   - multiple entries: map[string][]byte (entry name → content)
//
// Password-protected archives are supported both ways: reading legacy
// ZipCrypto (zip -P) and WinZip AES (method 99) entries, and writing WinZip
// AES-256 entries. Directories entries are skipped on unmarshal.
type ZipDataFormat struct {
	password     string
	entryName    string
	flat         bool
	comment      string
	modTime      time.Time
	maxEntrySize int64
	maxTotalSize int64
	maxEntries   int64
}

// ZipOption configures a ZipDataFormat.
type ZipOption func(*ZipDataFormat)

// ZipPassword sets the password used to encrypt entries while marshalling and
// to decrypt entries while unmarshalling.
func ZipPassword(password string) ZipOption {
	return func(f *ZipDataFormat) { f.password = password }
}

// ZipEntryName sets the entry name used when marshalling a single []byte or
// string body (default "data"). If the body carries a CamelFileName header
// value, that header takes precedence.
func ZipEntryName(name string) ZipOption {
	return func(f *ZipDataFormat) {
		if name != "" {
			f.entryName = name
		}
	}
}

// ZipFlat stores multi-entry bodies without their key directory structure,
// using only the base name of each key.
func ZipFlat(flat bool) ZipOption {
	return func(f *ZipDataFormat) { f.flat = flat }
}

// ZipComment sets the archive comment.
func ZipComment(comment string) ZipOption {
	return func(f *ZipDataFormat) { f.comment = comment }
}

// ZipModTime sets a fixed modification time for marshalled entries
// (default: the current time).
func ZipModTime(t time.Time) ZipOption {
	return func(f *ZipDataFormat) { f.modTime = t }
}

// ZipMaxEntrySize bounds the decompressed size of a single entry while
// unmarshalling (a zip bomb protection). Non-positive values disable the
// check; the default is 512 MiB. The limit applies per entry, not per archive.
func ZipMaxEntrySize(max int64) ZipOption {
	return func(f *ZipDataFormat) { f.maxEntrySize = max }
}

// ZipMaxTotalSize bounds the sum of decompressed entry sizes while
// unmarshalling. Non-positive values disable the check; the default is 1 GiB.
func ZipMaxTotalSize(max int64) ZipOption {
	return func(f *ZipDataFormat) { f.maxTotalSize = max }
}

// ZipMaxEntries bounds the number of regular-file entries while unmarshalling.
// Non-positive values disable the check; the default is 4096.
func ZipMaxEntries(max int64) ZipOption {
	return func(f *ZipDataFormat) { f.maxEntries = max }
}

// NewZipDataFormat creates a ZIP data format with the provided options.
func NewZipDataFormat(opts ...ZipOption) *ZipDataFormat {
	format := &ZipDataFormat{
		entryName:    "data",
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

// ZipFile is a named entry of a ZIP archive.
type ZipFile struct {
	Name    string
	Content []byte
}

// ZipFiles is an ordered collection of ZIP entries (map iteration order is
// randomised in Go, so slices of ZipFile are used when order matters).
type ZipFiles []ZipFile

// Marshal builds a ZIP archive from the body.
func (f *ZipDataFormat) Marshal(exchange *Exchange, body any) (any, error) {
	files, err := f.filesForMarshal(exchange, body)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("zip marshal: nothing to archive")
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if f.comment != "" {
		w.SetComment(f.comment)
	}
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.DefaultCompression)
	})

	modTime := f.modTime
	if modTime.IsZero() {
		modTime = time.Now()
	}

	for _, file := range files {
		name := file.Name
		if f.flat {
			name = path.Base(name)
		}
		if err := validateZipEntryName(name); err != nil {
			return nil, err
		}
		if f.password != "" {
			if err := f.writeAESEntry(w, name, file.Content, modTime); err != nil {
				return nil, err
			}
			continue
		}
		hdr := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: modTime,
		}
		fw, err := w.CreateHeader(hdr)
		if err != nil {
			return nil, fmt.Errorf("zip marshal: creating entry %q: %w", name, err)
		}
		if _, err := fw.Write(file.Content); err != nil {
			return nil, fmt.Errorf("zip marshal: writing entry %q: %w", name, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zip marshal: %w", err)
	}
	return buf.Bytes(), nil
}

// writeAESEntry writes one WinZip AE-2 AES-256 encrypted entry using the raw
// (pre-compressed, method 99) layout: the caller stores the payload as-is and
// declares method 99 with the 0x9901 extra field.
func (f *ZipDataFormat) writeAESEntry(w *zip.Writer, name string, content []byte, modTime time.Time) error {
	payload, extra, err := aesEncryptEntry(bytes.NewReader(content), f.password, zipAESStrength256)
	if err != nil {
		return fmt.Errorf("zip marshal: encrypting entry %q: %w", name, err)
	}

	// AES extra field: ID 0x9901 (little-endian), size, then vendor version
	// (2 = AE-2), vendor ID "AE", strength and the real (inner) method.
	extraField, err := aesExtraField(extra)
	if err != nil {
		return fmt.Errorf("zip marshal: entry %q: %w", name, err)
	}

	hdr := &zip.FileHeader{
		Name:     name,
		Method:   zipAESMethodPlaceholder, // method 99 marks AES entries
		Flags:    1,                       // bit 0: encrypted
		Modified: modTime,
		Extra:    extraField,
		// AE-2 stores no CRC-32; integrity is guaranteed by HMAC-SHA1.
		// Raw entries must declare their sizes up front.
		CompressedSize64:   uint64(len(payload)),
		UncompressedSize64: uint64(len(content)),
	}
	fw, err := w.CreateRaw(hdr)
	if err != nil {
		return fmt.Errorf("zip marshal: creating AES entry %q: %w", name, err)
	}
	if _, err := fw.Write(payload); err != nil {
		return fmt.Errorf("zip marshal: writing AES entry %q: %w", name, err)
	}
	return nil
}

// aesExtraField wraps the raw AES extra data in its 0x9901 container
// (header ID + data size little-endian).
func aesExtraField(data []byte) ([]byte, error) {
	size := len(data)
	if size < 0 || size > math.MaxUint16 {
		return nil, fmt.Errorf("zip aes: extra field too large (%d bytes)", size)
	}
	out := make([]byte, 0, 4+size)
	out = binary.LittleEndian.AppendUint16(out, zipAESExtraID)
	out = binary.LittleEndian.AppendUint16(out, uint16(size))
	return append(out, data...), nil
}

// filesForMarshal normalises the body into an ordered entry list.
func (f *ZipDataFormat) filesForMarshal(exchange *Exchange, body any) (ZipFiles, error) {
	switch v := body.(type) {
	case nil:
		return nil, fmt.Errorf("zip marshal: nil body")
	case string:
		return ZipFiles{{Name: f.nameFor(exchange), Content: []byte(v)}}, nil
	case []byte:
		return ZipFiles{{Name: f.nameFor(exchange), Content: v}}, nil
	case ZipFile:
		return ZipFiles{v}, nil
	case ZipFiles:
		return v, nil
	case []ZipFile:
		return v, nil
	case map[string]string:
		files := make(ZipFiles, 0, len(v))
		for name, content := range v {
			files = append(files, ZipFile{Name: name, Content: []byte(content)})
		}
		return files, nil
	case map[string][]byte:
		files := make(ZipFiles, 0, len(v))
		for name, content := range v {
			files = append(files, ZipFile{Name: name, Content: content})
		}
		return files, nil
	default:
		return nil, fmt.Errorf("zip marshal: unsupported body type %T (want []byte, string, ZipFile(s) or map[string][]byte)", body)
	}
}

// nameFor resolves the single-entry name, preferring the CamelFileName header.
func (f *ZipDataFormat) nameFor(exchange *Exchange) string {
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

// Unmarshal extracts entries from a ZIP archive.
func (f *ZipDataFormat) Unmarshal(exchange *Exchange, body any) (any, error) {
	data, err := bodyToBytes(body)
	if err != nil {
		return nil, fmt.Errorf("zip unmarshal: expects ZIP bytes, string or io.Reader: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("zip unmarshal: empty archive")
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("zip unmarshal: %w", err)
	}

	var (
		contents = make(map[string][]byte, len(zr.File))
		names    []string
		total    int64
	)
	maxEntries := f.maxEntries
	if maxEntries <= 0 {
		maxEntries = 1 << 30
	}
	maxTotal := f.maxTotalSize
	if maxTotal <= 0 {
		maxTotal = 1 << 62
	}
	for _, file := range zr.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if int64(len(names)+1) > maxEntries {
			return nil, fmt.Errorf("zip unmarshal: archive exceeds the maximum of %d entries (possible zip bomb)", maxEntries)
		}
		name, err := validateZipEntryReadName(file.Name)
		if err != nil {
			return nil, err
		}
		content, err := f.readFile(file)
		if err != nil {
			return nil, err
		}
		if total+int64(len(content)) > maxTotal {
			return nil, fmt.Errorf("zip unmarshal: archive exceeds the maximum total size of %d bytes (possible zip bomb)", maxTotal)
		}
		total += int64(len(content))
		contents[name] = content
		names = append(names, name)
	}

	if exchange != nil {
		exchange.GetIn().SetHeader(CamelZipEntryCount, len(names))
		exchange.GetIn().SetHeader(CamelZipEntryNames, names)
	}

	if len(contents) == 1 {
		return contents[names[0]], nil
	}
	return contents, nil
}

// readFile decrypts and decompresses a single archive entry.
func (f *ZipDataFormat) readFile(file *zip.File) ([]byte, error) {
	rc, err := f.openEntry(file)
	if err != nil {
		return nil, fmt.Errorf("zip unmarshal: opening entry %q: %w", file.Name, err)
	}
	defer rc.Close()

	limit := f.maxEntrySize
	if limit <= 0 {
		limit = 1 << 62
	}
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("zip unmarshal: reading entry %q: %w", file.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("zip unmarshal: entry %q exceeds the maximum size of %d bytes (possible zip bomb)", file.Name, limit)
	}
	if closeErr := rc.Close(); closeErr != nil {
		return nil, fmt.Errorf("zip unmarshal: authenticating entry %q: %w", file.Name, closeErr)
	}
	return data, nil
}

// openEntry returns a reader for the (possibly encrypted) entry.
func (f *ZipDataFormat) openEntry(file *zip.File) (io.ReadCloser, error) {
	encrypted := file.Flags&1 == 1

	// WinZip AES: method 99 with the 0x9901 extra field.
	if file.Method == zipAESMethodPlaceholder {
		extra, err := findAESExtra(file)
		if err != nil {
			return nil, err
		}
		if f.password == "" {
			return nil, fmt.Errorf("zip unmarshal: entry %q is AES-encrypted but no password was set (use ZipPassword)", file.Name)
		}
		return f.openAESEntry(file, extra)
	}

	if !encrypted {
		return file.Open()
	}
	if f.password == "" {
		return nil, fmt.Errorf("zip unmarshal: entry %q is password-protected but no password was set (use ZipPassword)", file.Name)
	}

	// Legacy ZipCrypto.
	raw, err := file.OpenRaw()
	if err != nil {
		return nil, err
	}
	zr := newZipCryptoReader(raw, f.password)
	// Password check byte (APPNOTE 6.1): the high byte of the CRC-32, or the
	// high byte of the MS-DOS modification time when bit 3 (data descriptor)
	// is set, because the CRC is not known in the local header in that case.
	check := byte(file.CRC32 >> 24)
	if file.Flags&0x8 != 0 {
		check = byte(file.ModifiedTime >> 8)
	}
	if err := zr.checkHeader(check); err != nil {
		return nil, err
	}
	switch file.Method {
	case zip.Store:
		return io.NopCloser(zr), nil
	case zip.Deflate:
		return flate.NewReader(zr), nil
	default:
		return nil, fmt.Errorf("zip unmarshal: entry %q uses unsupported compression method %d", file.Name, file.Method)
	}
}

const zipAESMethodPlaceholder = 99

// openAESEntry decrypts a WinZip AES entry, then decompresses the real method.
func (f *ZipDataFormat) openAESEntry(file *zip.File, extra *zipAESExtra) (io.ReadCloser, error) {
	if file.CompressedSize64 > math.MaxInt64 {
		return nil, fmt.Errorf("zip unmarshal: entry %q declares an invalid compressed size", file.Name)
	}
	raw, err := file.OpenRaw()
	if err != nil {
		return nil, err
	}
	compSize := int64(file.CompressedSize64)
	ae, err := newZipAESReader(raw, f.password, extra, compSize)
	if err != nil {
		return nil, err
	}
	switch extra.realMethod {
	case zip.Store:
		return &aesStoreReadCloser{ae: ae}, nil
	case zip.Deflate:
		return &aesDeflateReadCloser{r: flate.NewReader(ae), ae: ae}, nil
	default:
		return nil, fmt.Errorf("zip unmarshal: entry %q uses unsupported inner compression method %d", file.Name, extra.realMethod)
	}
}

type aesDeflateReadCloser struct {
	r  io.ReadCloser
	ae *zipAESReader
}

func (d *aesDeflateReadCloser) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err == io.EOF {
		if d.ae.remaining != 0 {
			return n, errors.New("zip aes: compressed stream ended before consuming full payload")
		}
		if macErr := d.ae.finishMAC(); macErr != nil {
			return n, macErr
		}
	}
	return n, err
}

func (d *aesDeflateReadCloser) Close() error {
	flateErr := d.r.Close()
	if d.ae.remaining != 0 {
		return errors.New("zip aes: stream closed before consuming full payload")
	}
	if macErr := d.ae.finishMAC(); macErr != nil {
		return macErr
	}
	return flateErr
}

type aesStoreReadCloser struct {
	ae *zipAESReader
}

func (s *aesStoreReadCloser) Read(p []byte) (int, error) {
	return s.ae.Read(p)
}

func (s *aesStoreReadCloser) Close() error {
	if s.ae.remaining != 0 {
		return errors.New("zip aes: stream closed before consuming full payload")
	}
	return s.ae.finishMAC()
}

// findAESExtra locates and parses the 0x9901 extra field of an entry.
func findAESExtra(file *zip.File) (*zipAESExtra, error) {
	data := file.Extra
	for len(data) >= 4 {
		fieldID := uint16(data[0]) | uint16(data[1])<<8
		size := int(data[2]) | int(data[3])<<8
		if len(data) < 4+size {
			break
		}
		if fieldID == zipAESExtraID {
			return parseAESExtra(data[4 : 4+size])
		}
		data = data[4+size:]
	}
	return nil, fmt.Errorf("zip unmarshal: entry %q declares AES but carries no 0x9901 extra field", file.Name)
}

// validateZipEntryName rejects entry names that could escape the archive root
// when written to disk (zip-slip), or that contain null bytes.
func validateZipEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("zip marshal: empty entry name")
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("zip marshal: entry name %q contains a null byte", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("zip marshal: entry name %q escapes the archive root", name)
	}
	if path.IsAbs(clean) || strings.HasPrefix(clean, "/") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return fmt.Errorf("zip marshal: entry name %q is absolute", name)
	}
	return nil
}

// validateZipEntryReadName validates an entry name read from an archive:
// names that would escape the extraction root (zip-slip) and names with null
// bytes are rejected outright rather than silently renamed, because
// downstream processors may write entries to disk under the map key.
func validateZipEntryReadName(name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("zip unmarshal: entry name %q contains a null byte", name)
	}
	if name == "" {
		return "", fmt.Errorf("zip unmarshal: empty entry name")
	}
	if containsTraversal(name) {
		return "", fmt.Errorf("zip unmarshal: entry name %q escapes the archive root", name)
	}
	clean := path.Clean(name)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("zip unmarshal: invalid entry name %q", name)
	}
	return clean, nil
}
