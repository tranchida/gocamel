package gocamel

import (
	"bytes"
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
