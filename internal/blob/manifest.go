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

// the chunk size when none is given, 4 MiB, e.g. a 1 GiB file is 256 chunks
// about the same as BitTorrent and OCI registries use
const DefaultChunkSize = 4 << 20 // 4 MiB

// one chunk in a manifest: where it sits, how big it is and what it should hash to
type ChunkInfo struct {
	Index  int    `json:"index"`
	Offset int64  `json:"offset"` // byte offset into the original blob (0 + n * DefaultChunkSize)
	Length int    `json:"length"` // bytes in a chunk
	SHA256 string `json:"sha256"` // hex-encoded
}

// a file described as a list of chunks, in order
// sent as JSON before any chunk, so the receiver can say yes or no up front
//
//	// report.pdf, 10 MiB, with the default 4 MiB chunks
//	{"name":"report.pdf","media_type":"application/octet-stream","size":10485760,"sha256":"9c1e…",
//	 "chunks":[
//	   {"index":0,"offset":0,"length":4194304,"sha256":"4f0a…"},
//	   {"index":1,"offset":4194304,"length":4194304,"sha256":"b27d…"},
//	   {"index":2,"offset":8388608,"length":2097152,"sha256":"e83c…"}]}   // the last chunk is shorter
//
//	// an empty file has no chunks at all
//	{"name":"empty.txt","media_type":"application/octet-stream","size":0,
//	 "sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","chunks":null}
type Manifest struct {
	Name      string      `json:"name,omitempty"` // the sender's filename, e.g. report.pdf
	MediaType string      `json:"media_type"`     // e.g. application/octet-stream
	Size      int64       `json:"size"`           // total blob size, sum of chunk lengths
	SHA256    string      `json:"sha256"`         // hash of the whole file, catches a chunk written in the wrong place
	Chunks    []ChunkInfo `json:"chunks"`
}

// reads the file once and hashes every chunk and the whole file
// e.g. a 10 MiB file with 4 MiB chunks gives 3 chunks: 4, 4 and 2 MiB
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
		// a short last chunk and the end of the file both mean we're done, not an error
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

// the longest filename we keep from a peer, 120 bytes
// well under most filesystems' 255, leaving room for a prefix and extension
const maxNameLen = 120

// turns a filename from a peer into one safe file name, or "" if nothing usable is left
// the peer can send anything, so a path in the name must never leave the inbox
//
//	"report.pdf"                    → "report.pdf"
//	"../../.ssh/authorized_keys"    → "authorized_keys"
//	`C:\Users\me\notes.txt`        → "notes.txt"     // Windows path, cut on Linux too
//	".bashrc"                       → "bashrc"        // no hidden files
//	"what?.txt"                     → "what.txt"      // ? is reserved on Windows
//	"nul.txt"                       → "_nul.txt"      // NUL is a device on Windows
//	".."                            → ""              // the inbox then calls it "blob"
func SafeName(name string) string {
	// split on both / and \ on every OS, since a Windows sender can send a backslash path to Linux
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

	// CON, NUL, COM1 and so on are devices on Windows, even as "nul.txt", so prefix them
	stem, _, _ := strings.Cut(out, ".")
	if windowsReserved[strings.ToUpper(stem)] {
		out = "_" + out
	}

	if len(out) > maxNameLen {
		out = out[:maxNameLen]
		// don't cut a multi-byte character in half
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

// the most chunks a manifest may list, about a million
// a small manifest could still list millions of tiny chunks and make us build a huge map
const maxChunks = 1 << 20 // 1048576 chunks

// checks the chunks cover the file exactly once, in order, with no gaps
// or a peer could say 1 KB and send a chunk at 1 TB, making a 1 TB file
//
//	the report.pdf manifest above                        → nil
//	size -1                                              → "blob: manifest size -1 is negative"
//	chunks listed 0, 2, 1                                → "blob: chunk at position 1 has index 2"
//	chunk 1 at offset 5000000 instead of 4194304         → "blob: chunk 1 starts at 5000000, want 4194304"
//	chunk 0 of 100 MiB                                   → "blob: chunk 0 length 104857600 exceeds max 67108864"
//	size 1024 but one chunk of 4194304                   → "blob: chunks total 4194304 bytes, manifest size is 1024"
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
