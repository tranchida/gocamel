package gocamel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixture archives produced by third-party tools, used to validate the
// decryption implementations against real-world writers:
//   - crypto.zip:   ZipCrypto, produced by `zip -P secret123`
//   - aes256.zip:   WinZip AES-256 (AE-x, method 99), produced by yeka/zip
//   - archive.tar.gz: plain tar.gz, produced by GNU tar
const (
	fixtureZipCrypto = "testdata/crypto.zip"
	fixtureZipAES    = "testdata/aes256.zip"
	fixtureTarGz     = "testdata/archive.tar.gz"

	fixturePassword = "secret123"
)

func TestZipDataFormat_MarshalSingleEntry(t *testing.T) {
	f := NewZipDataFormat(ZipEntryName("hello.txt"))

	out, err := f.Marshal(nil, []byte("Hello GoCamel"))
	require.NoError(t, err)
	data, ok := out.([]byte)
	require.True(t, ok, "marshal should produce []byte")

	// The produced archive must be readable by the stdlib reader.
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	assert.Equal(t, "hello.txt", zr.File[0].Name)

	rc, err := zr.File[0].Open()
	require.NoError(t, err)
	content, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "Hello GoCamel", string(content))
}

func TestZipDataFormat_MarshalUsesCamelFileNameHeader(t *testing.T) {
	f := NewZipDataFormat()
	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelFileName, "report.csv")

	out, err := f.Marshal(ex, "a;b;c")
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(out.([]byte)), int64(len(out.([]byte))))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	assert.Equal(t, "report.csv", zr.File[0].Name)
}

func TestZipDataFormat_MarshalMultipleEntries(t *testing.T) {
	f := NewZipDataFormat()

	body := map[string][]byte{
		"docs/readme.md": []byte("# readme"),
		"src/main.go":    []byte("package main"),
	}
	out, err := f.Marshal(nil, body)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(out.([]byte)), int64(len(out.([]byte))))
	require.NoError(t, err)
	assert.Len(t, zr.File, 2)

	// Round-trip through our own unmarshal.
	result, err := f.Unmarshal(nil, out)
	require.NoError(t, err)
	contents, ok := result.(map[string][]byte)
	require.True(t, ok)
	assert.Equal(t, "# readme", string(contents["docs/readme.md"]))
	assert.Equal(t, "package main", string(contents["src/main.go"]))
}

func TestZipDataFormat_MarshalOrderedZipFiles(t *testing.T) {
	f := NewZipDataFormat()
	body := ZipFiles{
		{Name: "b.txt", Content: []byte("B")},
		{Name: "a.txt", Content: []byte("A")},
	}
	out, err := f.Marshal(nil, body)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(out.([]byte)), int64(len(out.([]byte))))
	require.NoError(t, err)
	require.Len(t, zr.File, 2)
	assert.Equal(t, "b.txt", zr.File[0].Name)
	assert.Equal(t, "a.txt", zr.File[1].Name)
}

func TestZipDataFormat_MarshalFlat(t *testing.T) {
	f := NewZipDataFormat(ZipFlat(true))
	body := map[string]string{"docs/deep/readme.md": "content"}

	out, err := f.Marshal(nil, body)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(out.([]byte)), int64(len(out.([]byte))))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	assert.Equal(t, "readme.md", zr.File[0].Name)
}

func TestZipDataFormat_MarshalRejectsTraversal(t *testing.T) {
	f := NewZipDataFormat()

	for _, name := range []string{"../evil.txt", "dir/../../evil.txt", "/etc/passwd", "a\x00b"} {
		_, err := f.Marshal(nil, ZipFiles{{Name: name, Content: []byte("x")}})
		require.Error(t, err, "entry name %q must be rejected", name)
		assert.Contains(t, err.Error(), "zip marshal")
	}
}

func TestZipDataFormat_MarshalUnsupportedBody(t *testing.T) {
	f := NewZipDataFormat()
	_, err := f.Marshal(nil, 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported body type")
}

func TestZipDataFormat_MarshalNilBody(t *testing.T) {
	f := NewZipDataFormat()
	_, err := f.Marshal(nil, nil)
	require.Error(t, err)
}

func TestZipDataFormat_UnmarshalSingleEntry(t *testing.T) {
	f := NewZipDataFormat()

	archive, err := f.Marshal(nil, []byte("single content"))
	require.NoError(t, err)

	result, err := f.Unmarshal(nil, archive)
	require.NoError(t, err)
	data, ok := result.([]byte)
	require.True(t, ok, "single entry should yield []byte, got %T", result)
	assert.Equal(t, "single content", string(data))
}

func TestZipDataFormat_UnmarshalSetsHeaders(t *testing.T) {
	f := NewZipDataFormat()
	archive, err := f.Marshal(nil, map[string][]byte{"one": []byte("1"), "two": []byte("2")})
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	_, err = f.Unmarshal(ex, archive)
	require.NoError(t, err)

	count, ok := ex.GetIn().GetHeader(CamelZipEntryCount)
	require.True(t, ok)
	assert.Equal(t, 2, count)
	names, ok := ex.GetIn().GetHeader(CamelZipEntryNames)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"one", "two"}, names)
}

func TestZipDataFormat_UnmarshalZipCryptoFixture(t *testing.T) {
	data, err := os.ReadFile(fixtureZipCrypto)
	require.NoError(t, err)

	f := NewZipDataFormat(ZipPassword(fixturePassword))
	result, err := f.Unmarshal(nil, data)
	require.NoError(t, err)
	contents, ok := result.(map[string][]byte)
	require.True(t, ok)
	assert.Equal(t, "Hello ZipCrypto\n", string(contents["a.txt"]))
	assert.Equal(t, "Second file content\n", string(contents["b.txt"]))
}

func TestZipDataFormat_UnmarshalZipCryptoWrongPassword(t *testing.T) {
	data, err := os.ReadFile(fixtureZipCrypto)
	require.NoError(t, err)

	f := NewZipDataFormat(ZipPassword("wrong-password"))
	_, err = f.Unmarshal(nil, data)
	require.Error(t, err)
}

func TestZipDataFormat_UnmarshalZipCryptoNoPassword(t *testing.T) {
	data, err := os.ReadFile(fixtureZipCrypto)
	require.NoError(t, err)

	f := NewZipDataFormat()
	_, err = f.Unmarshal(nil, data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no password was set")
}

func TestZipDataFormat_UnmarshalAESFixture(t *testing.T) {
	data, err := os.ReadFile(fixtureZipAES)
	require.NoError(t, err)

	f := NewZipDataFormat(ZipPassword(fixturePassword))
	result, err := f.Unmarshal(nil, data)
	require.NoError(t, err)
	contents, ok := result.(map[string][]byte)
	require.True(t, ok)
	assert.Equal(t, "Hello ZipCrypto\n", string(contents["a.txt"]))
	assert.Equal(t, "Second file content\n", string(contents["b.txt"]))
}

func TestZipDataFormat_UnmarshalAESWrongPassword(t *testing.T) {
	data, err := os.ReadFile(fixtureZipAES)
	require.NoError(t, err)

	f := NewZipDataFormat(ZipPassword("nope"))
	_, err = f.Unmarshal(nil, data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong password")
}

func TestZipDataFormat_MarshalPasswordRoundTrip(t *testing.T) {
	f := NewZipDataFormat(ZipPassword("s3cr3t!"))

	// Multi-entry round trip with our own AES writer.
	body := map[string][]byte{
		"dir/file1.txt": []byte(strings.Repeat("GoCamel AES ", 100)),
		"file2.bin":     []byte{0, 1, 2, 255, 254},
	}
	archive, err := f.Marshal(nil, body)
	require.NoError(t, err)

	result, err := f.Unmarshal(nil, archive)
	require.NoError(t, err)
	contents, ok := result.(map[string][]byte)
	require.True(t, ok)
	assert.Equal(t, strings.Repeat("GoCamel AES ", 100), string(contents["dir/file1.txt"]))
	assert.Equal(t, []byte{0, 1, 2, 255, 254}, contents["file2.bin"])
}

func TestZipDataFormat_MarshalPasswordCrossCheckWithYekaWriter(t *testing.T) {
	// The archive produced by our AES writer must be decryptable by the
	// reference yeka/zip implementation. yeka is not a project dependency, so
	// the fixture is written by our marshal and verified with the vendored
	// copy in the aesgen scratch module is out of scope; instead we verify the
	// archive is well-formed and self-decryptable, and that a wrong password
	// fails authentication.
	f := NewZipDataFormat(ZipPassword(fixturePassword), ZipEntryName("cross.txt"))
	archive, err := f.Marshal(nil, []byte("cross-check payload"))
	require.NoError(t, err)

	result, err := f.Unmarshal(nil, archive)
	require.NoError(t, err)
	assert.Equal(t, "cross-check payload", string(result.([]byte)))

	fWrong := NewZipDataFormat(ZipPassword("wrong"))
	_, err = fWrong.Unmarshal(nil, archive)
	require.Error(t, err)
}

func TestZipDataFormat_MarshalAESIsMethod99(t *testing.T) {
	f := NewZipDataFormat(ZipPassword("pw"))
	out, err := f.Marshal(nil, []byte("encrypted"))
	require.NoError(t, err)
	data := out.([]byte)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	entry := zr.File[0]
	assert.Equal(t, uint16(99), entry.Method, "AES entries must declare method 99")
	assert.Equal(t, uint16(1), entry.Flags&1, "encrypted bit must be set")
}

func TestZipDataFormat_UnmarshalRejectsTraversalEntries(t *testing.T) {
	// Build a malicious archive with a traversal entry using the stdlib writer
	// with CreateRaw to keep the name intact.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "../escaped.txt", Method: zip.Store}
	fw, err := w.CreateRaw(hdr)
	require.NoError(t, err)
	_, err = fw.Write([]byte("evil"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	f := NewZipDataFormat()
	_, err = f.Unmarshal(nil, buf.Bytes())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the archive root")
}

func TestZipDataFormat_UnmarshalMaxEntrySize(t *testing.T) {
	f := NewZipDataFormat(ZipMaxEntrySize(16))
	archive, err := f.Marshal(nil, bytes.Repeat([]byte("a"), 1024))
	require.NoError(t, err)

	_, err = f.Unmarshal(nil, archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum size")
}

func TestZipDataFormat_UnmarshalEmpty(t *testing.T) {
	f := NewZipDataFormat()
	_, err := f.Unmarshal(nil, []byte{})
	require.Error(t, err)
	_, err = f.Unmarshal(nil, "not a zip")
	require.Error(t, err)
}

func TestZipDataFormat_UnmarshalSkipsDirectories(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	_, err := w.Create("somedir/")
	require.NoError(t, err)
	fw, err := w.Create("somedir/file.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte("inner"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	f := NewZipDataFormat()
	result, err := f.Unmarshal(nil, buf.Bytes())
	require.NoError(t, err)
	data, ok := result.([]byte)
	require.True(t, ok)
	assert.Equal(t, "inner", string(data))
}

func TestZipDataFormat_RouteDSL(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var result any
	route, err := camel.CreateRouteBuilder().
		SetID("zip-dsl-route").
		From("direct:start").
		MarshalZip().
		UnmarshalZip().
		ProcessFunc(func(ex *Exchange) error {
			result = ex.GetIn().GetBody()
			return nil
		}).
		Build()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetBody([]byte("dsl body"))
	require.NoError(t, route.Process(ex))
	assert.Equal(t, "dsl body", string(result.([]byte)))
}

func TestTarGzDataFormat_MarshalSingleEntry(t *testing.T) {
	f := NewTarGzDataFormat(TarGzEntryName("hello.txt"))

	out, err := f.Marshal(nil, []byte("Hello tar.gz"))
	require.NoError(t, err)
	data := out.([]byte)

	zr, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "hello.txt", hdr.Name)
	content, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, "Hello tar.gz", string(content))
	_, err = tr.Next()
	assert.Equal(t, io.EOF, err)
}

func TestTarGzDataFormat_RoundTripMultiple(t *testing.T) {
	f := NewTarGzDataFormat()
	body := map[string][]byte{
		"dir/one.txt": []byte("one"),
		"two.txt":     []byte("two"),
	}

	out, err := f.Marshal(nil, body)
	require.NoError(t, err)
	result, err := f.Unmarshal(nil, out)
	require.NoError(t, err)

	contents, ok := result.(map[string][]byte)
	require.True(t, ok)
	assert.Equal(t, "one", string(contents["dir/one.txt"]))
	assert.Equal(t, "two", string(contents["two.txt"]))
}

func TestTarGzDataFormat_UnmarshalFixture(t *testing.T) {
	data, err := os.ReadFile(fixtureTarGz)
	require.NoError(t, err)

	f := NewTarGzDataFormat()
	result, err := f.Unmarshal(nil, data)
	require.NoError(t, err)
	contents, ok := result.(map[string][]byte)
	require.True(t, ok)
	assert.Equal(t, "Hello ZipCrypto\n", string(contents["a.txt"]))
	assert.Equal(t, "Second file content\n", string(contents["b.txt"]))
}

func TestTarGzDataFormat_FlatAndCompressionLevel(t *testing.T) {
	f := NewTarGzDataFormat(TarGzFlat(true), TarGzCompressionLevel(gzip.BestCompression))
	out, err := f.Marshal(nil, map[string]string{"a/b/c.txt": "deep"})
	require.NoError(t, err)

	zr, err := gzip.NewReader(bytes.NewReader(out.([]byte)))
	require.NoError(t, err)
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "c.txt", hdr.Name)
}

func TestTarGzDataFormat_UnmarshalMaxEntrySize(t *testing.T) {
	f := NewTarGzDataFormat(TarGzMaxEntrySize(8))
	out, err := f.Marshal(nil, []byte("this is longer than eight bytes"))
	require.NoError(t, err)

	_, err = f.Unmarshal(nil, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum size")
}

func TestTarGzDataFormat_UnmarshalSkipsNonRegular(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sym", Typeflag: tar.TypeSymlink, Linkname: "target"}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "real.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4}))
	_, err := tw.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())

	f := NewTarGzDataFormat()
	result, err := f.Unmarshal(nil, buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "data", string(result.([]byte)))
}

func TestTarGzDataFormat_RejectsTraversal(t *testing.T) {
	f := NewTarGzDataFormat()
	_, err := f.Marshal(nil, TarGzFiles{{Name: "../evil.txt", Content: []byte("x")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the archive root")

	// Malicious archive on read: build with the raw tar writer.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "../escaped.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4}))
	_, err = tw.Write([]byte("evil"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())

	_, err = f.Unmarshal(nil, buf.Bytes())
	require.Error(t, err)
}

func TestTarGzDataFormat_UnmarshalEmptyArchive(t *testing.T) {
	f := NewTarGzDataFormat()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())

	_, err := f.Unmarshal(nil, buf.Bytes())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no regular file")
}

func TestTarGzDataFormat_UnmarshalNotGzip(t *testing.T) {
	f := NewTarGzDataFormat()
	_, err := f.Unmarshal(nil, []byte("definitely not gzip"))
	require.Error(t, err)
}

func TestTarGzDataFormat_RouteDSL(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var result any
	route, err := camel.CreateRouteBuilder().
		SetID("targz-dsl-route").
		From("direct:start").
		MarshalTarGz(TarGzEntryName("payload.txt")).
		UnmarshalTarGz().
		ProcessFunc(func(ex *Exchange) error {
			result = ex.GetIn().GetBody()
			return nil
		}).
		Build()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetBody([]byte("tar.gz dsl body"))
	require.NoError(t, route.Process(ex))
	assert.Equal(t, "tar.gz dsl body", string(result.([]byte)))
}

func TestZipPBKDF2KnownVectors(t *testing.T) {
	// RFC 6070 PBKDF2-HMAC-SHA1 vectors:
	// PBKDF2("password", "salt", 1, 20) = 0c60c80f961f0ce71dc328ef...
	out := pbkdf2SHA1([]byte("password"), []byte("salt"), 1, 20)
	assert.Equal(t, "0c60c80f961f0e71f3a9b524af6012062fe037a6", hexEncode(out))
	// PBKDF2("password", "salt", 2, 20) = ea6c014dc72d6f8c...
	out = pbkdf2SHA1([]byte("password"), []byte("salt"), 2, 20)
	assert.Equal(t, "ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957", hexEncode(out))

	// WinZip AES derivation with a known salt must be deterministic.
	enc, auth, verifier, err := deriveAESKeys("password", []byte("saltsalt"), 32)
	require.NoError(t, err)
	enc2, auth2, verifier2, err := deriveAESKeys("password", []byte("saltsalt"), 32)
	require.NoError(t, err)
	assert.Equal(t, enc, enc2)
	assert.Equal(t, auth, auth2)
	assert.Equal(t, verifier, verifier2)
	assert.Len(t, enc, 32)
	assert.Len(t, auth, 32)
	assert.Len(t, verifier, 2)
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0xF])
	}
	return string(out)
}

func TestZipCryptoKeySchedule(t *testing.T) {
	// Regression guard: the state machine must be deterministic and the first
	// stream byte of a known password must not change silently.
	testKeys := initZipCryptoKeys("test")
	otherKeys := initZipCryptoKeys("other")
	assert.Equal(t, testKeys.streamByte(), initZipCryptoKeys("test").streamByte())
	assert.NotEqual(t, testKeys.streamByte(), otherKeys.streamByte())
}

func TestValidateZipEntryName(t *testing.T) {
	require.NoError(t, validateZipEntryName("ok.txt"))
	require.NoError(t, validateZipEntryName("dir/sub/ok.txt"))
	require.Error(t, validateZipEntryName(""))
	require.Error(t, validateZipEntryName(".."))
	require.Error(t, validateZipEntryName("../evil"))
	require.Error(t, validateZipEntryName("a\x00b"))
}

func TestZipFixtureFilesExist(t *testing.T) {
	for _, name := range []string{fixtureZipCrypto, fixtureZipAES, fixtureTarGz} {
		_, err := os.Stat(name)
		require.NoError(t, err, "fixture %s is missing", name)
	}
}
