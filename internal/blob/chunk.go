// Responsible for sending and receiving blob packets over network
package blob

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Max size one frame can commit in the memory at once (so that no one can claim 100GB at once or something like that)
const maxChunkSize = 64 << 20 // 64 MiB

// Progress reports how far a transfer has got. BytesTotal and ChunksTotal
// come from the manifest, so both are known before the first chunk moves.
type Progress struct {
	ChunksDone  int
	ChunksTotal int
	BytesDone   int64
	BytesTotal  int64

	// Verifying is set once every chunk has arrived and the receiver is
	// flushing to disk and re-reading the file to check its whole-blob hash.
	// That takes real time on a large file, during which a progress bar at
	// 100% looks like a hang.
	Verifying bool
}

// Frac returns completion in 0..1, or 0 for an empty blob.
func (p Progress) Frac() float64 {
	if p.BytesTotal <= 0 {
		return 0
	}
	return float64(p.BytesDone) / float64(p.BytesTotal)
}

// ProgressFunc is called once per chunk. It runs on the transfer goroutine,
// so it must not block — push to a counter or a channel, don't render.
// A nil ProgressFunc is fine and costs nothing.
type ProgressFunc func(Progress)

func (f ProgressFunc) report(p Progress) {
	if f != nil {
		f(p)
	}
}

// Reads one chunk at a time
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

// WriteChunk writes one framed chunk: [4-byte index][4-byte length][bytes]
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

// Send writes the manifest, waits for the receiver's verdict, then writes
// every chunk of path in order. A receiver that declines yields a
// *RefusedError before any chunk moves. onProgress may be nil.
//
// rw is one handle onto two one-way pipes, and the two sides take turns:
//
//	sender                                      receiver
//	WriteManifest ──── outgoing pipe ────────>  ReadManifest
//	ReadVerdict   <─── incoming pipe ─────────  WriteVerdict   (parks here)
//	WriteChunk x N ─── outgoing pipe ────────>  ReadChunk x N
//
// Read more: docs/reading/two-pipes.md, and the notes at the bottom of this file.
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

// AcceptFunc is a receiver's policy. It sees the manifest a peer is offering
// and returns where to write the blob, or an error to refuse. The error's text
// is sent to the peer as the reason, so it must be safe for them to see.
type AcceptFunc func(m *Manifest) (destPath string, err error)

// Receive reads the manifest, checks it is self-consistent, asks accept
// whether to take it, answers the sender with a verdict, then reads every
// chunk — verifying each one before it touches disk. onProgress may be nil.
// It is the mirror image of Send: see the diagram there.
func Receive(rw io.ReadWriter, accept AcceptFunc, onProgress ProgressFunc) (*Manifest, error) {
	m, err := ReadManifest(rw)
	if err != nil {
		return nil, err
	}

	// Refuse before allocating anything: Size and Offset drive Truncate and
	// WriteAt below, so a manifest that lies about them is rejected here.
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

	// Tell the caller the size up front, so a receiver can show a full-width
	// bar at 0% instead of nothing until the first chunk lands.
	onProgress.report(Progress{ChunksTotal: len(m.Chunks), BytesTotal: m.Size})

	// Read exactly as many frames as the manifest promised, no more.
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

// verifyWhole re-reads the finished file and check it against the manifest's
// whole blob hash. Per chunk hashes prove each piece is intact; only this catches if a piece
// is landing at wrong place
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

// ---------------------------------------------------------------------------
// Notes: how the bytes move
//
// A 4 TB file never sits in memory, and never "sits in the TCP stream". Four
// ideas make that true. Each has a full write-up, with diagrams, under
// docs/reading/ — start at docs/reading/README.md.
//
// 1. Two pipes — docs/reading/two-pipes.md
//    A connection is two independent one-way byte streams. net.Conn is one
//    handle onto both, and io.ReadWriter is just the interface saying "has
//    Read and Write" — which is why Send and Receive run unchanged over a
//    real tailnet connection and over net.Pipe() in tests. The sender's wait
//    for the verdict is nothing special: ReadVerdict is an io.ReadFull on a
//    pipe with nothing in it yet, and the goroutine parks until bytes
//    arrive, the connection closes, or the deadline passes.
//
// 2. Framing — docs/reading/framing.md
//    TCP has no message boundaries, so every frame carries its own length,
//    and io.ReadFull reads exactly that many bytes and never more. That is
//    what keeps the cursor aligned across ReadManifest, ReadVerdict and
//    every ReadChunk.
//
// 3. Flow control — docs/reading/flow-control.md
//    Send reads one chunk from disk at a time, and Write blocks when the
//    receiver has no room, so a slow receiver slows the sender with nothing
//    written to make it so. Live memory is about one chunk buffer per end.
//
// 4. The receive window — docs/reading/receive-window.md
//    The number the receiver's stack advertises, which is what actually makes
//    Write block. Owned by the receiver, never the sender.
//
// The one rule to carry into any new message: two pipes rule out byte-level
// collisions, not deadlock. A message both sides could send at once needs an
// explicit order or its own reader goroutine — see two-pipes.md.
// ---------------------------------------------------------------------------
