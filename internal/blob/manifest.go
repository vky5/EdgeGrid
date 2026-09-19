package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
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
		MediaType: mediaType,
		Size:      offset,
		SHA256:    hex.EncodeToString(whole.Sum(nil)), // checksum of entire file 
		Chunks:    chunks, 
	}, nil
}
