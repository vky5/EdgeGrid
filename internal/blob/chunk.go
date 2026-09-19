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

// Send writes the manifest to w, then every chunk of path in order.
func Send(w io.Writer, path string, m *Manifest) error {
	if err := WriteManifest(w, m); err != nil { // manifest is sent first over io writer
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("blob: open %s: %w", path, err)
	}
	defer f.Close()

	var buf []byte
	for _, c := range m.Chunks {
		if cap(buf) < c.Length {
			buf = make([]byte, c.Length)
		}
		buf = buf[:c.Length]
		if _, err := io.ReadFull(f, buf); err != nil {
			return fmt.Errorf("blob: read chunk %d from %s: %w", c.Index, path, err)
		}
		if err := WriteChunk(w, c.Index, buf); err != nil {
			return err
		}
	}
	return nil
}

// Receive reads the manifest from r, then every chunk it promises, verifying
// each one before it touches disk.
func Receive(r io.Reader, destPath string) (*Manifest, error) {
	m, err := ReadManifest(r)
	if err != nil {
		return nil, err
	}

	want := make(map[int]ChunkInfo, len(m.Chunks)) // this is all the chunk we need
	for _, c := range m.Chunks {
		want[c.Index] = c
	}

	f, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("blob: create %s: %w", destPath, err)
	}

	defer f.Close()

	if err := f.Truncate(m.Size); err != nil {
		return nil, fmt.Errorf("blob: truncate %s: %w", destPath, err)
	}

	seen := make(map[int]bool, len(m.Chunks))

	// Read exactly as many frames as the manifest promised, no more.
	for i := 0; i < len(m.Chunks); i++ {
		idx, data, err := ReadChunk(r)
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
	}

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
// How this stays bounded: framing, io.ReadFull, and TCP flow control
//
// A 4 TB file never sits anywhere in memory, and never "sits in the TCP
// stream" either. Three separate mechanisms make that true.
//
// 1. Framing — length prefixes make a byte stream parseable
//
// TCP delivers an ordered stream of bytes with no message boundaries. Write
// 4 MiB in one call and the receiver may see it as 900 reads of 4 KiB; write
// three small messages and they may arrive coalesced into one read. So the
// receiver can only find message boundaries if the messages carry their own
// lengths. That is what every frame here does:
//
//	manifest:  [4-byte length][JSON body]
//	chunk:     [4-byte index][4-byte length][raw bytes]   (repeated)
//
// The order is fixed — manifest first, then exactly len(m.Chunks) chunk
// frames — so nothing needs a type tag. Position in the stream is the type.
//
// 2. io.ReadFull — reads exactly N bytes, never more
//
// io.Reader.Read is allowed to return fewer bytes than asked for. ReadFull
// loops until the buffer is exactly full, and critically it never reads
// past it. That is what keeps the cursor aligned across calls:
//
//	ReadManifest:  read 4 bytes  -> N          read N bytes  -> JSON
//	               (cursor is now exactly on the first chunk header)
//	ReadChunk:     read 8 bytes  -> idx, len   read len bytes -> data
//	               (cursor is now exactly on the next chunk header)
//
// Each call consumes its own frame and stops. No delimiters, no lookahead,
// no scanning for markers.
//
// Note: if the connection is ever wrapped in a bufio.Reader, that same
// reader must be threaded through every call. bufio reads ahead into its
// own buffer, so mixing a wrapped reader with the raw conn loses whatever
// is sitting in the buffer and desyncs the stream.
//
// 3. TCP flow control — the sender blocks when the receiver is slow
//
// The receiver advertises a window: how many bytes it is willing to accept
// right now. As Receive's loop hashes and writes a chunk, it is not
// draining the socket, so that window shrinks. When it reaches zero, the
// sender's w.Write blocks inside Send. The remaining data stays on the
// sender's disk, read one chunk at a time by io.ReadFull(f, buf).
//
// Nobody wrote that backpressure — it falls out of the kernel refusing to
// accept bytes the receiver has no room for. So at any instant:
//
//	sender:     one chunk buffer (4 MiB, reused) + socket send buffer
//	in flight:  about one receive window
//	receiver:   one chunk buffer (4 MiB)         + socket recv buffer
//
// Roughly 10 MB of live memory, whether the file is 40 MB or 4 TB.
//
// Worked example — a 10 GiB file at the 4 MiB default chunk size:
//
//	BuildManifest reads it once, producing 2560 ChunkInfo entries
//	(~150 bytes each, so a ~380 KB manifest). No file data is retained.
//
//	Send writes the manifest, reopens the file, then loops 2560 times:
//	    read 4 MiB from disk into buf (reused every iteration)
//	    write [index][length][4 MiB] to the socket, blocking if the
//	    receiver is behind
//
//	Receive reads the manifest, learns to expect exactly 2560 frames, then
//	loops 2560 times:
//	    ReadChunk fills a fresh 4 MiB buffer
//	    sha256 it and compare against m.Chunks[i].SHA256
//	    WriteAt(data, c.Offset) — absolute position, so order never matters
//
//	Finally verifyWhole re-reads the assembled file. Per-chunk hashes prove
//	each piece arrived intact; only this catches a piece written to the
//	wrong place.
//
// Why chunk at all, given TCP already segments into ~1400-byte packets:
// chunks are not packets and buy nothing at the network layer. They buy
// verification granularity (which 4 MiB is bad, not merely "something is"),
// resume (restart at chunk 87, not byte 0), cheap refetch (re-request
// 4 MiB, not 10 GiB), and — once a request protocol exists — the ability to
// fetch indices out of order or from several peers at once, which WriteAt
// already supports.
// ---------------------------------------------------------------------------
