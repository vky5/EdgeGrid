// Responsible for sending and receiving blob packets over network 
package blob

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)


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
