// sends and receives files over a connection, one chunk at a time

package blob

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// the biggest chunk we'll hold in memory, 64 MiB, so a peer can't make us allocate 100 GB
const maxChunkSize = 64 << 20 // 64 MiB

// how far a transfer has got
// the totals come from the manifest, so they're known before the first chunk moves
type Progress struct {
	ChunksDone  int
	ChunksTotal int
	BytesDone   int64
	BytesTotal  int64

	// all chunks are in and the whole file is being checked
	// that takes a while on a big file, so without this the bar sits at 100% and looks stuck
	Verifying bool
}

// how much is done, from 0 to 1; 0 for an empty file
func (p Progress) Frac() float64 {
	if p.BytesTotal <= 0 {
		return 0
	}
	return float64(p.BytesDone) / float64(p.BytesTotal)
}

// called after each chunk; it must return fast, or it slows the transfer
type ProgressFunc func(Progress)

func (f ProgressFunc) report(p Progress) {
	if f != nil {
		f(p)
	}
}

// reads one chunk: index, length, then the bytes
func ReadChunk(r io.Reader) (index int, data []byte, err error) {
	var header [8]byte // [index][length]
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, fmt.Errorf("blob: read chunk header: %w", err)
	}

	idx := binary.BigEndian.Uint32(header[:4])     // Index of packet
	length := binary.BigEndian.Uint32(header[4:8]) // length of the buffer

	if length > maxChunkSize {
		return 0, nil, fmt.Errorf("blob: chunk %d length %d exceeds max %d", idx, length, maxChunkSize)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, fmt.Errorf("blob: read chunk %d body: %w", idx, err)
	}

	return int(idx), buf, nil
}

// writes one chunk: [4-byte index][4-byte length][bytes]
func WriteChunk(w io.Writer, index int, data []byte) error {
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(index))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("blob: write chunk %d header: %w", index, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("blob: write chunk %d body: %w", index, err)
	}
	return nil
}

// sends the manifest, waits for yes or no, then sends every chunk in order
// a no comes back as *RefusedError before any chunk moves
//
// the two sides take turns on one connection:
//
//	sender                                      receiver
//	WriteManifest ──── outgoing pipe ────────>  ReadManifest
//	ReadVerdict   <─── incoming pipe ─────────  WriteVerdict   (parks here)
//	WriteChunk x N ─── outgoing pipe ────────>  ReadChunk x N
//
// more: docs/reading/two-pipes.md
func Send(rw io.ReadWriter, path string, m *Manifest, onProgress ProgressFunc) error {
	if err := WriteManifest(rw, m); err != nil { // manifest is sent first
		return err
	}
	if err := ReadVerdict(rw); err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("blob: open %s: %w", path, err)
	}
	defer f.Close()

	var buf []byte
	var sent int64
	for i, c := range m.Chunks {
		if cap(buf) < c.Length {
			buf = make([]byte, c.Length)
		}
		buf = buf[:c.Length]
		if _, err := io.ReadFull(f, buf); err != nil {
			return fmt.Errorf("blob: read chunk %d from %s: %w", c.Index, path, err)
		}
		if err := WriteChunk(rw, c.Index, buf); err != nil {
			return err
		}
		sent += int64(c.Length)
		onProgress.report(Progress{
			ChunksDone: i + 1, ChunksTotal: len(m.Chunks),
			BytesDone: sent, BytesTotal: m.Size,
		})
	}
	return nil
}

// decides whether to take an offered file, and where to save it
// the error text is sent to the peer as the reason, so it must be safe for them to see
type AcceptFunc func(m *Manifest) (destPath string, err error)

// reads the manifest, checks it, asks accept, answers yes or no, then reads every chunk
// each chunk is checked against its hash before it's written (the other half of Send)
func Receive(rw io.ReadWriter, accept AcceptFunc, onProgress ProgressFunc) (*Manifest, error) {
	m, err := ReadManifest(rw)
	if err != nil {
		return nil, err
	}

	// check the manifest before making the file, since its sizes decide how big the file gets
	if err := m.Validate(); err != nil {
		_ = WriteVerdict(rw, err)
		return nil, err
	}

	destPath, err := accept(m)
	if err != nil {
		_ = WriteVerdict(rw, err)
		return nil, fmt.Errorf("blob: refused: %w", err)
	}
	if err := WriteVerdict(rw, nil); err != nil {
		return nil, err
	}

	want := make(map[int]ChunkInfo, len(m.Chunks)) // this is all the chunk we need
	for _, c := range m.Chunks {
		want[c.Index] = c
	}

	f, err := os.OpenFile(destPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("blob: create %s: %w", destPath, err)
	}

	defer f.Close()

	if err := f.Truncate(m.Size); err != nil {
		return nil, fmt.Errorf("blob: truncate %s: %w", destPath, err)
	}

	seen := make(map[int]bool, len(m.Chunks))
	var got int64

	// report the size up front, so the bar shows at 0% before the first chunk lands
	onProgress.report(Progress{ChunksTotal: len(m.Chunks), BytesTotal: m.Size})

	// read exactly as many chunks as the manifest promised, no more
	for i := 0; i < len(m.Chunks); i++ {
		idx, data, err := ReadChunk(rw)
		if err != nil {
			return nil, err
		}

		c, ok := want[idx]
		if !ok {
			return nil, fmt.Errorf("blob: chunk %d is not in the manifest", idx)
		}

		if seen[idx] {
			return nil, fmt.Errorf("blob: chunk %d sent twice", idx)
		}

		if len(data) != c.Length {
			return nil, fmt.Errorf("blob: chunk %d is %d bytes, manifest says %d", idx, len(data), c.Length)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != c.SHA256 {
			return nil, fmt.Errorf("blob: chunk %d hash mismatch: got %s, want %s", idx, got, c.SHA256)
		}
		if _, err := f.WriteAt(data, c.Offset); err != nil {
			return nil, fmt.Errorf("blob: write chunk %d: %w", idx, err)
		}
		seen[idx] = true
		got += int64(c.Length)
		onProgress.report(Progress{
			ChunksDone: i + 1, ChunksTotal: len(m.Chunks),
			BytesDone: got, BytesTotal: m.Size,
		})
	}

	onProgress.report(Progress{
		ChunksDone: len(m.Chunks), ChunksTotal: len(m.Chunks),
		BytesDone: got, BytesTotal: m.Size,
		Verifying: true,
	})

	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("blob: sync %s: %w", destPath, err)
	}

	if err := verifyWhole(f, m.SHA256); err != nil {
		return nil, err
	}

	return m, nil

}

// re-reads the finished file and checks it against the whole-file hash
// chunk hashes prove each piece is right; only this catches a piece written in the wrong place
func verifyWhole(f *os.File, want string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("blob: whole-blob hash mismatch: got %s, want %s", got, want)
	}

	return nil
}

// how the bytes move (two pipes, framing, flow control): docs/reading/README.md
