package gocamel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZipAESReader_PartialReadsAndTruncation(t *testing.T) {
	plain := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	payload, extra, err := aesEncryptEntry(bytes.NewReader(plain), "pw", zipAESStrength256)
	require.NoError(t, err)
	ae, err := parseAESExtra(extra)
	require.NoError(t, err)

	t.Run("one-byte reads", func(t *testing.T) {
		r, err := newZipAESReader(bytes.NewReader(payload), "pw", ae, int64(len(payload)))
		require.NoError(t, err)
		var got []byte
		buf := make([]byte, 1)
		for {
			n, err := r.Read(buf)
			got = append(got, buf[:n]...)
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
		}
		assert.Equal(t, plain, got)
	})

	t.Run("large buffer", func(t *testing.T) {
		r, err := newZipAESReader(bytes.NewReader(payload), "pw", ae, int64(len(payload)))
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, plain, got)
	})

	t.Run("empty payload", func(t *testing.T) {
		empty, extraEmpty, err := aesEncryptEntry(bytes.NewReader(nil), "pw", zipAESStrength256)
		require.NoError(t, err)
		aeEmpty, err := parseAESExtra(extraEmpty)
		require.NoError(t, err)
		r, err := newZipAESReader(bytes.NewReader(empty), "pw", aeEmpty, int64(len(empty)))
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("truncated ciphertext", func(t *testing.T) {
		require.Greater(t, len(payload), 12)
		trunc := payload[:len(payload)-5]
		r, err := newZipAESReader(bytes.NewReader(trunc), "pw", ae, int64(len(payload)))
		require.NoError(t, err)
		_, err = io.ReadAll(r)
		require.Error(t, err)
	})

	t.Run("premature EOF inside payload returns ErrUnexpectedEOF", func(t *testing.T) {
		require.Greater(t, len(payload), 25)
		trunc := payload[:len(payload)-15]
		r, err := newZipAESReader(bytes.NewReader(trunc), "pw", ae, int64(len(payload)))
		require.NoError(t, err)
		_, err = io.ReadAll(r)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})
}

func TestZipDataFormat_MarshalRejectsAbsoluteName(t *testing.T) {
	f := NewZipDataFormat()
	_, err := f.Marshal(nil, ZipFiles{{Name: "/etc/passwd", Content: []byte("x")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestTarGzDataFormat_MarshalRejectsAbsoluteName(t *testing.T) {
	f := NewTarGzDataFormat()
	_, err := f.Marshal(nil, TarGzFiles{{Name: "/etc/passwd", Content: []byte("x")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestZipDataFormat_UnmarshalMaxEntries(t *testing.T) {
	f := NewZipDataFormat()
	body := map[string][]byte{"a": []byte("1"), "b": []byte("2"), "c": []byte("3")}
	archive, err := f.Marshal(nil, body)
	require.NoError(t, err)

	limited := NewZipDataFormat(ZipMaxEntries(2))
	_, err = limited.Unmarshal(nil, archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum of 2 entries")
}

func TestZipDataFormat_UnmarshalMaxTotalSize(t *testing.T) {
	f := NewZipDataFormat()
	body := map[string][]byte{"a": []byte("hello"), "b": []byte("world")}
	archive, err := f.Marshal(nil, body)
	require.NoError(t, err)

	limited := NewZipDataFormat(ZipMaxTotalSize(7))
	_, err = limited.Unmarshal(nil, archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum total size")
}

func TestTarGzDataFormat_UnmarshalMaxEntries(t *testing.T) {
	f := NewTarGzDataFormat()
	body := map[string][]byte{"a": []byte("1"), "b": []byte("2"), "c": []byte("3")}
	archive, err := f.Marshal(nil, body)
	require.NoError(t, err)

	limited := NewTarGzDataFormat(TarGzMaxEntries(2))
	_, err = limited.Unmarshal(nil, archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum of 2 entries")
}

func TestTarGzDataFormat_UnmarshalMaxTotalSize(t *testing.T) {
	f := NewTarGzDataFormat()
	body := map[string][]byte{"a": []byte("hello"), "b": []byte("world")}
	archive, err := f.Marshal(nil, body)
	require.NoError(t, err)

	limited := NewTarGzDataFormat(TarGzMaxTotalSize(7))
	_, err = limited.Unmarshal(nil, archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum total size")
}

func TestTarGz_NonRegularBombAndMaxTotalBudget(t *testing.T) {
	t.Run("rejects more than maxEntries TypeDir headers", func(t *testing.T) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		for i := range 6 {
			require.NoError(t, tw.WriteHeader(&tar.Header{
				Name:     "dir" + string(rune('a'+i)) + "/",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
			}))
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "ok.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     2,
		}))
		_, err := tw.Write([]byte("ok"))
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		require.NoError(t, zw.Close())

		f := NewTarGzDataFormat(TarGzMaxEntries(5))
		_, err = f.Unmarshal(nil, buf.Bytes())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum of 5 entries")
	})

	t.Run("rejects huge TypeCont entry before decompressing past budget", func(t *testing.T) {
		// Craft a tar header declaring a 1 GiB TypeCont entry followed by only
		// 2 KiB of zero padding inside a valid gzip stream. If Unmarshal skips
		// TypeCont without checking hdr.Size against remainingBudget, tr.Next()
		// will fail with unexpected EOF after trying to skip 1 GiB instead of
		// rejecting immediately with maximum total size.
		var rawTar bytes.Buffer
		tw := tar.NewWriter(&rawTar)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "huge-cont",
			Typeflag: tar.TypeCont,
			Mode:     0o644,
			Size:     1 << 30,
		}))
		// Flush only the 512-byte header without writing 1 GiB body, plus 2 KiB.
		var gzipBuf bytes.Buffer
		zw := gzip.NewWriter(&gzipBuf)
		_, err := zw.Write(rawTar.Bytes())
		require.NoError(t, err)
		_, err = zw.Write(make([]byte, 2048))
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		f := NewTarGzDataFormat(TarGzMaxTotalSize(1024))
		_, err = f.Unmarshal(nil, gzipBuf.Bytes())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum total size")
		assert.False(t, errors.Is(err, io.ErrUnexpectedEOF))
	})

	t.Run("rejects valid TypeCont bomb followed by regular file", func(t *testing.T) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		contPayload := bytes.Repeat([]byte("A"), 64*1024)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "cont.bin",
			Typeflag: tar.TypeCont,
			Mode:     0o644,
			Size:     int64(len(contPayload)),
		}))
		_, err := tw.Write(contPayload)
		require.NoError(t, err)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "ok.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     2,
		}))
		_, err = tw.Write([]byte("hi"))
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		require.NoError(t, zw.Close())

		f := NewTarGzDataFormat(TarGzMaxTotalSize(1024))
		_, err = f.Unmarshal(nil, buf.Bytes())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum total size")
	})

	t.Run("TarGzMaxTotalSize(1024) with default maxEntrySize stops before reading 10 MiB payload", func(t *testing.T) {
		// Header claims 10 MiB (below default 512 MiB maxEntrySize) with only
		// 2048 bytes actually written inside gzip. Must reject via total budget
		// without reading past 1025 bytes and hitting unexpected EOF.
		var rawTar bytes.Buffer
		tw := tar.NewWriter(&rawTar)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "big.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     10 << 20,
		}))
		var gzipBuf bytes.Buffer
		zw := gzip.NewWriter(&gzipBuf)
		_, err := zw.Write(rawTar.Bytes())
		require.NoError(t, err)
		_, err = zw.Write(make([]byte, 2048))
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		f := NewTarGzDataFormat(TarGzMaxTotalSize(1024))
		_, err = f.Unmarshal(nil, gzipBuf.Bytes())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum total size")
		assert.False(t, errors.Is(err, io.ErrUnexpectedEOF))
	})
}

func TestArchive_SortedNamesDuplicatesAndEmptyHeaders(t *testing.T) {
	t.Run("targz empty archive does not set exchange headers", func(t *testing.T) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "onlydir/", Typeflag: tar.TypeDir, Mode: 0o755}))
		require.NoError(t, tw.Close())
		require.NoError(t, zw.Close())

		ex := NewExchange(context.Background())
		_, err := NewTarGzDataFormat().Unmarshal(ex, buf.Bytes())
		require.Error(t, err)
		_, hasCount := ex.GetIn().GetHeader(CamelZipEntryCount)
		_, hasNames := ex.GetIn().GetHeader(CamelZipEntryNames)
		assert.False(t, hasCount)
		assert.False(t, hasNames)
	})

	t.Run("zip and targz sort CamelZipEntryNames and unify duplicate handling", func(t *testing.T) {
		// Unsorted multi-entry ZIP
		var zipBuf bytes.Buffer
		zw := zip.NewWriter(&zipBuf)
		for _, name := range []string{"z.txt", "a.txt", "m.txt"} {
			fw, err := zw.Create(name)
			require.NoError(t, err)
			_, err = fw.Write([]byte(name))
			require.NoError(t, err)
		}
		require.NoError(t, zw.Close())

		exZip := NewExchange(context.Background())
		_, err := NewZipDataFormat().Unmarshal(exZip, zipBuf.Bytes())
		require.NoError(t, err)
		names, ok := exZip.GetIn().GetHeader(CamelZipEntryNames)
		require.True(t, ok)
		assert.Equal(t, []string{"a.txt", "m.txt", "z.txt"}, names)

		// Unsorted multi-entry TarGz
		tarOut, err := NewTarGzDataFormat().Marshal(nil, TarGzFiles{
			{Name: "z.txt", Content: []byte("z")},
			{Name: "a.txt", Content: []byte("a")},
			{Name: "m.txt", Content: []byte("m")},
		})
		require.NoError(t, err)
		exTar := NewExchange(context.Background())
		_, err = NewTarGzDataFormat().Unmarshal(exTar, tarOut)
		require.NoError(t, err)
		tarNames, ok := exTar.GetIn().GetHeader(CamelZipEntryNames)
		require.True(t, ok)
		assert.Equal(t, []string{"a.txt", "m.txt", "z.txt"}, tarNames)

		// Duplicate entry in ZIP & TarGz
		var dupZip bytes.Buffer
		dzw := zip.NewWriter(&dupZip)
		for _, val := range []string{"first", "second"} {
			fw, werr := dzw.Create("dup.txt")
			require.NoError(t, werr)
			_, werr = fw.Write([]byte(val))
			require.NoError(t, werr)
		}
		require.NoError(t, dzw.Close())

		exDupZip := NewExchange(context.Background())
		resZip, err := NewZipDataFormat().Unmarshal(exDupZip, dupZip.Bytes())
		require.NoError(t, err)
		assert.Equal(t, []byte("second"), resZip)
		cZip, _ := exDupZip.GetIn().GetHeader(CamelZipEntryCount)
		assert.Equal(t, 1, cZip)
		nZip, _ := exDupZip.GetIn().GetHeader(CamelZipEntryNames)
		assert.Equal(t, []string{"dup.txt"}, nZip)

		dupTar, err := NewTarGzDataFormat().Marshal(nil, TarGzFiles{
			{Name: "dup.txt", Content: []byte("first")},
			{Name: "dup.txt", Content: []byte("second")},
		})
		require.NoError(t, err)
		exDupTar := NewExchange(context.Background())
		resTar, err := NewTarGzDataFormat().Unmarshal(exDupTar, dupTar)
		require.NoError(t, err)
		assert.Equal(t, []byte("second"), resTar)
		cTar, _ := exDupTar.GetIn().GetHeader(CamelZipEntryCount)
		assert.Equal(t, 1, cTar)
		nTar, _ := exDupTar.GetIn().GetHeader(CamelZipEntryNames)
		assert.Equal(t, []string{"dup.txt"}, nTar)
	})
}
