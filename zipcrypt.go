package gocamel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- mandated by WinZip AES (PBKDF2-HMAC-SHA1, HMAC-SHA1); not a design choice
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// This file implements the two password-protection schemes commonly found in
// ZIP archives, using only the standard library:
//
//   - ZipCrypto (legacy PKWARE stream cipher, methods 1..99 with the general
//     purpose bit 0 set): decrypt-only. The cipher is weak by design, but it
//     is still the default of the `zip -P` command-line tool.
//   - WinZip AES (AE-1 and AE-2, compression method 99): encrypt and decrypt,
//     with AES-128/192/256 plus an HMAC-SHA1 authentication code.
//
// The ZipCrypto key schedule follows the PKWARE APPNOTE (section 6.1) and the
// reference implementations of the CRC-32 based state machine.

// zipCryptoKeys holds the three 32-bit state words of the legacy PKWARE cipher.
type zipCryptoKeys [3]uint32

// initZipCryptoKeys seeds the cipher state from the password, using the CRC-32
// update primitive described in the APPNOTE.
func initZipCryptoKeys(password string) zipCryptoKeys {
	var keys zipCryptoKeys
	keys[0] = 0x12345678
	keys[1] = 0x23456789
	keys[2] = 0x34567890
	for i := 0; i < len(password); i++ {
		keys.update(byte(password[i]))
	}
	return keys
}

// update advances the cipher state with a single plaintext byte, following
// the PKWARE APPNOTE (section 6.1):
//
//	keys[0] = crc32(keys[0], byte)
//	keys[1] = keys[1] + (keys[0] & 0xff)
//	keys[1] = keys[1] * 134775813 + 1
//	keys[2] = crc32(keys[2], keys[1] >> 24)
func (k *zipCryptoKeys) update(b byte) {
	k[0] = crc32UpdateByte(k[0], b)
	k[1] = (k[1] + (k[0] & 0xFF)) & 0xFFFFFFFF
	k[1] = (k[1]*134775813 + 1) & 0xFFFFFFFF
	k[2] = crc32UpdateByte(k[2], byte(k[1]>>24))
}

// crc32UpdateByte advances a CRC-32 IEEE state by one byte using the
// precomputed table, matching the PKWARE reference implementation.
func crc32UpdateByte(crc uint32, b byte) uint32 {
	return crc32.IEEETable[(crc^uint32(b))&0xFF] ^ (crc >> 8)
}

// streamByte returns the pseudo-random byte that XORs the next ciphertext byte.
func (k zipCryptoKeys) streamByte() byte {
	temp := (k[2] | 2) & 0xFFFF
	return byte((temp * (temp ^ 1)) >> 8) // #nosec G115 -- intentional truncation to the low byte (PKWARE spec)
}

// decryptByte decrypts one byte and updates the state with the plaintext.
func (k *zipCryptoKeys) decryptByte(c byte) byte {
	p := c ^ k.streamByte()
	k.update(p)
	return p
}

// errZipCryptoPassword signals that the 12-byte encryption header check failed,
// which almost always means the password is wrong.
var errZipCryptoPassword = errors.New("wrong password or corrupted ZipCrypto header")

// zipCryptoReader decrypts a ZipCrypto-protected stream. The first 12 bytes of
// the stream are the encryption header; the last byte of that header (or the
// high byte of the CRC-32 for method 99 entries) must match after decryption.
type zipCryptoReader struct {
	r     io.Reader
	keys  zipCryptoKeys
	plain []byte // buffered decrypted header remainder
}

func newZipCryptoReader(r io.Reader, password string) *zipCryptoReader {
	return &zipCryptoReader{
		r:    r,
		keys: initZipCryptoKeys(password),
	}
}

// checkHeader reads and decrypts the 12-byte encryption header and verifies the
// password check byte. expectedCheck is the high byte of the entry CRC-32, or
// the high byte of the MS-DOS modification time when the data descriptor bit
// is set (the caller resolves which one applies).
func (z *zipCryptoReader) checkHeader(expectedCheck byte) error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(z.r, header); err != nil {
		return fmt.Errorf("zipcrypto: short encryption header: %w", err)
	}
	for i, c := range header {
		header[i] = z.keys.decryptByte(c)
	}
	if header[11] != expectedCheck {
		return errZipCryptoPassword
	}
	return nil
}

func (z *zipCryptoReader) Read(p []byte) (int, error) {
	if len(z.plain) > 0 {
		n := copy(p, z.plain)
		z.plain = z.plain[n:]
		return n, nil
	}
	n, err := z.r.Read(p)
	if n > 0 {
		for i := 0; i < n; i++ {
			p[i] = z.keys.decryptByte(p[i])
		}
	}
	return n, err
}

// WinZip AES constants (AES extra field, method 99).
const (
	zipAESExtraID       = 0x9901 // stored little-endian as bytes 01 99
	zipAESVendorAE1     = 1      // AE-1: CRC-32 stored in the central directory
	zipAESVendorAE2     = 2      // AE-2: no CRC, HMAC-SHA1 provides integrity
	zipAESStrength128   = 0x01
	zipAESStrength192   = 0x02
	zipAESStrength256   = 0x03
	zipAESPasswordError = "zip aes: wrong password or corrupted data (authentication failed)" // #nosec G101 -- error message, not a credential
)

// zipAESExtra is the parsed content of the WinZip AES extra field (0x9901).
type zipAESExtra struct {
	vendorVersion int // 1 = AE-1, 2 = AE-2
	vendorID      [2]byte
	strength      int    // 1, 2 or 3 (AES-128/192/256)
	realMethod    uint16 // underlying compression method
}

func parseAESExtra(data []byte) (*zipAESExtra, error) {
	if len(data) < 7 {
		return nil, fmt.Errorf("zip aes: extra field too short (%d bytes)", len(data))
	}
	e := &zipAESExtra{
		vendorVersion: int(data[0]) | int(data[1])<<8,
		vendorID:      [2]byte{data[2], data[3]},
		strength:      int(data[4]),
		realMethod:    uint16(data[5]) | uint16(data[6])<<8,
	}
	if e.vendorVersion != zipAESVendorAE1 && e.vendorVersion != zipAESVendorAE2 {
		return nil, fmt.Errorf("zip aes: unsupported vendor version %d", e.vendorVersion)
	}
	if e.strength < zipAESStrength128 || e.strength > zipAESStrength256 {
		return nil, fmt.Errorf("zip aes: unsupported strength %d", e.strength)
	}
	return e, nil
}

// aesKeySizes maps the AES strength byte to key/nonce/mac sizes in bytes.
func aesKeySizes(strength int) (keyLen, saltLen, macLen int, err error) {
	switch strength {
	case zipAESStrength128:
		return 16, 8, 10, nil
	case zipAESStrength192:
		return 24, 12, 10, nil
	case zipAESStrength256:
		return 32, 16, 10, nil
	default:
		return 0, 0, 0, fmt.Errorf("zip aes: unsupported strength %d", strength)
	}
}

// deriveAESKeys derives the WinZip AES key material with PBKDF2-HMAC-SHA1
// (1000 iterations): the total derivation length is keyLen*2 + 2 (encryption
// key, authentication key of the same length, then a 2-byte password
// verifier), matching the WinZip AES specification and the yeka/zip
// reference implementation.
func deriveAESKeys(password string, salt []byte, keyLen int) (encKey, authKey, verifier []byte, err error) {
	out := pbkdf2SHA1([]byte(password), salt, 1000, keyLen*2+2)
	if len(out) < keyLen*2+2 {
		return nil, nil, nil, fmt.Errorf("zip aes: key derivation failed")
	}
	encKey = out[:keyLen]
	authKey = out[keyLen : keyLen*2]
	verifier = out[keyLen*2 : keyLen*2+2]
	return encKey, authKey, verifier, nil
}

// pbkdf2SHA1 implements PBKDF2 with HMAC-SHA1 per RFC 2898.
func pbkdf2SHA1(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		// U1 = PRF(password, salt || INT(block))
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		u = prf.Sum(u[:0])
		t := make([]byte, hashLen)
		copy(t, u)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// winZipCTR implements the WinZip AES counter mode: a 16-byte little-endian,
// left-aligned counter that starts at 1 and increments from byte 0. This is
// NOT the NIST CTR layout used by cipher.NewCTR, so a dedicated stream is
// required.
type winZipCTR struct {
	b   cipher.Block
	ctr [16]byte
	out [16]byte
	off int // next unused keystream index; 16 means the block is spent
}

func newWinZipCTR(key []byte) (*winZipCTR, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("zip aes: %w", err)
	}
	c := &winZipCTR{b: block, off: 16}
	c.ctr[0] = 1 // counter starts at 1
	return c, nil
}

func (c *winZipCTR) XORKeyStream(dst, src []byte) {
	for i := 0; i < len(src); i++ {
		if c.off == 16 {
			c.b.Encrypt(c.out[:], c.ctr[:])
			c.off = 0
			// Increment the counter little-endian, starting at byte 0.
			for j := 0; j < len(c.ctr); j++ {
				c.ctr[j]++
				if c.ctr[j] != 0 {
					break
				}
			}
		}
		dst[i] = src[i] ^ c.out[c.off]
		c.off++
	}
}

// zipAESReader decrypts and authenticates a WinZip AES (AE-1/AE-2) entry body.
type zipAESReader struct {
	data      io.Reader
	stream    cipher.Stream
	mac       hash.Hash
	remaining int64 // compressed payload bytes left before the 10-byte MAC
	macDone   bool
}

func newZipAESReader(r io.Reader, password string, extra *zipAESExtra, compressedSize int64) (*zipAESReader, error) {
	keyLen, saltLen, macLen, err := aesKeySizes(extra.strength)
	if err != nil {
		return nil, err
	}
	if compressedSize < int64(saltLen+2+macLen) {
		return nil, fmt.Errorf("zip aes: entry too small for AES metadata (%d bytes)", compressedSize)
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(r, salt); err != nil {
		return nil, fmt.Errorf("zip aes: reading salt: %w", err)
	}
	encKey, authKey, verifier, err := deriveAESKeys(password, salt, keyLen)
	if err != nil {
		return nil, err
	}
	fileVerifier := make([]byte, 2)
	if _, err := io.ReadFull(r, fileVerifier); err != nil {
		return nil, fmt.Errorf("zip aes: reading password verifier: %w", err)
	}
	if subtle.ConstantTimeCompare(verifier, fileVerifier) != 1 {
		return nil, errors.New(zipAESPasswordError)
	}
	stream, err := newWinZipCTR(encKey)
	if err != nil {
		return nil, err
	}
	payloadSize := compressedSize - int64(saltLen) - 2 - int64(macLen)
	return &zipAESReader{
		data:      r,
		stream:    stream,
		mac:       hmac.New(sha1.New, authKey),
		remaining: payloadSize,
	}, nil
}

func (z *zipAESReader) finishMAC() error {
	if z.macDone {
		return nil
	}
	z.macDone = true
	return z.verifyMAC()
}

func (z *zipAESReader) Read(p []byte) (int, error) {
	if z.remaining == 0 {
		if err := z.finishMAC(); err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if int64(len(p)) > z.remaining {
		p = p[:z.remaining]
	}
	n, err := z.data.Read(p)
	if n > 0 {
		z.remaining -= int64(n)
		if _, macErr := z.mac.Write(p[:n]); macErr != nil {
			return n, macErr
		}
		z.stream.XORKeyStream(p[:n], p[:n])
	}
	if z.remaining == 0 && (err == nil || err == io.EOF) {
		if macErr := z.finishMAC(); macErr != nil {
			return n, macErr
		}
	}
	return n, err
}

func (z *zipAESReader) verifyMAC() error {
	expected := make([]byte, 10)
	if _, err := io.ReadFull(z.data, expected); err != nil {
		return fmt.Errorf("zip aes: reading authentication code: %w", err)
	}
	if !hmac.Equal(expected, z.mac.Sum(nil)[:10]) {
		return errors.New(zipAESPasswordError)
	}
	return nil
}

// aesEncryptEntry produces a WinZip AE-2 (AES-256 by default) encrypted entry.
// AE-2 stores no CRC; the HMAC provides integrity instead.
func aesEncryptEntry(r io.Reader, password string, strength int) (data []byte, extra []byte, err error) {
	keyLen, saltLen, macLen, err := aesKeySizes(strength)
	if err != nil {
		return nil, nil, err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, fmt.Errorf("zip aes: generating salt: %w", err)
	}
	encKey, authKey, verifier, err := deriveAESKeys(password, salt, keyLen)
	if err != nil {
		return nil, nil, err
	}
	stream, err := newWinZipCTR(encKey)
	if err != nil {
		return nil, nil, err
	}
	mac := hmac.New(sha1.New, authKey)

	var out []byte
	out = append(out, salt...)
	out = append(out, verifier...)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			// The HMAC authenticates the ciphertext (the CTR keystream is
			// applied before the data reaches the MAC, matching the WinZip
			// AES specification and the yeka/zip reference).
			stream.XORKeyStream(chunk, chunk)
			mac.Write(chunk)
			out = append(out, chunk...)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("zip aes: encrypting payload: %w", readErr)
		}
	}
	out = append(out, mac.Sum(nil)[:macLen]...)

	extra = make([]byte, 7)
	extra[0] = zipAESVendorAE2 // vendor version: 2 = AE-2 (no CRC stored)
	extra[1] = 0
	extra[2] = 'A'
	extra[3] = 'E'
	extra[4] = byte(strength) // #nosec G115 -- strength validated to 1..3 by aesKeySizes above
	extra[5] = 0              // real method: Store (0) — the payload is stored by the caller
	extra[6] = 0
	return out, extra, nil
}
