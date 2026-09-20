package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// DefaultChunkSize is used when BuildManifest isn't given an explicit one.
// 4 MiB sits in the same range BitTorrent and OCI registries typically use.
const DefaultChunkSize = 4 << 20 // 4 MiB

// ChunkInfo is one entry in a Manifest: what a chunk should hash to and how
// big it is, decided before any transfer happens.
type ChunkInfo struct {
	Index  int    `json:"index"`
	Offset int64  `json:"offset"` // byte offset into the original blob (0 + n * DefaultChunkSize)
	Length int    `json:"length"` // bytes in a chunk
	SHA256 string `json:"sha256"` // hex-encoded
}

// Manifest describes one blob as an ordered list of chunks.
type Manifest struct {
	Name      string      `json:"name,omitempty"` // Name is the sender's own filename for this blob
	MediaType string      `json:"media_type"`
	Size      int64       `json:"size"`   // total blob size, sum of chunk lengths
	SHA256    string      `json:"sha256"` // hash of the whole blob — belt-and-suspenders on top of per-chunk hashes
	Chunks    []ChunkInfo `json:"chunks"`
}

// BuildManifest reads path once, start to finish, and produces a Manifest
// every chunk hashed, plus a hash of the whole blob
func BuildManifest(path, mediaType string, chunkSize int) (*Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("blob: open %s: %w", path, err)
	}
	defer f.Close()

	whole := sha256.New()
	buf := make([]byte, chunkSize) // load only the chunk in memory that you are gonna hash not 10 GB entirely
	var chunks []ChunkInfo
	var offset int64
	index := 0

	for {
		// io.ReadFull tries to fill buf completely. It returns
		// ErrUnexpectedEOF for a short final read (the last, smaller
		// chunk) and EOF only once there's truly nothing left — both are
		// expected end-of-file conditions here, not errors, which is why
		// they're handled below instead of returned.
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			chunkHash := sha256.Sum256(buf[:n])
			chunks = append(chunks, ChunkInfo{
				Index:  index,
				Offset: offset,
				Length: n,
				SHA256: hex.EncodeToString(chunkHash[:]),
			})
			whole.Write(buf[:n]) // like a calculator feeding it all the chunks
			offset += int64(n)
			index++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("blob: read %s: %w", path, err)
		}
	}

	return &Manifest{
		Name:      filepath.Base(path),
		MediaType: mediaType,
		Size:      offset,
		SHA256:    hex.EncodeToString(whole.Sum(nil)), // checksum of entire file
		Chunks:    chunks,
	}, nil
}

// maxNameLen bounds a received filename well under the 255-byte limit most
// filesystems impose, leaving room for a prefix and extension.
const maxNameLen = 120

// SafeName turns a peer-supplied filename into one that is safe to use as a
// single path component. Name comes off the wire, so it can be anything:
// "../../.ssh/authorized_keys", "C:\Windows\x", a NUL byte, an empty string.
// It returns "" when nothing usable is left, in which case the caller falls
// back to a name of its own.
func SafeName(name string) string {
	// Treat both separators as separators no matter which OS this is:
	// a sender on Windows can hand a Linux receiver a backslash path, and
	// filepath.Base on Linux would leave the backslashes in place.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f: // control characters, including NUL
		case strings.ContainsRune(`<>:"|?*`, r): // reserved on Windows
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ".") // no "..", no hidden-file trick, no trailing dot on Windows

	// CON, NUL, COM1 and friends are device names on Windows, with or
	// without an extension: opening "nul.txt" there does not create a file.
	stem, _, _ := strings.Cut(out, ".")
	if windowsReserved[strings.ToUpper(stem)] {
		out = "_" + out
	}

	if len(out) > maxNameLen {
		out = out[:maxNameLen]
		// Don't leave a split multi-byte rune at the cut.
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

// maxChunks bounds how many chunks a manifest may declare. maxManifestSize
// already caps the JSON, but a compact manifest could still list millions of
// tiny chunks and make the receiver build a map that large.
const maxChunks = 1 << 20 // 1048576 chunks

// Validate checks that a manifest describes one contiguous, self-consistent
// blob. A manifest arrives from a peer, and Receive trusts it for two things
// that would otherwise be attack surface: how big to make the file, and
// where each chunk gets written. Without this, a small Size plus a chunk at
// offset 1 TB makes WriteAt extend the file to a terabyte.
func (m *Manifest) Validate() error {
	if m.Size < 0 {
		return fmt.Errorf("blob: manifest size %d is negative", m.Size)
	}
	if len(m.Chunks) > maxChunks {
		return fmt.Errorf("blob: manifest declares %d chunks, max %d", len(m.Chunks), maxChunks)
	}

	var offset int64
	for i, c := range m.Chunks {
		switch {
		case c.Index != i:
			return fmt.Errorf("blob: chunk at position %d has index %d", i, c.Index)
		case c.Length <= 0:
			return fmt.Errorf("blob: chunk %d has length %d", i, c.Length)
		case c.Length > maxChunkSize:
			return fmt.Errorf("blob: chunk %d length %d exceeds max %d", i, c.Length, maxChunkSize)
		case c.Offset != offset:
			return fmt.Errorf("blob: chunk %d starts at %d, want %d", i, c.Offset, offset)
		}
		offset += int64(c.Length)
	}
	if offset != m.Size {
		return fmt.Errorf("blob: chunks total %d bytes, manifest size is %d", offset, m.Size)
	}
	return nil
}

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}
