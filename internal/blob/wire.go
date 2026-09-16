package blob

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)


  const maxManifestSize = 64 << 20 // 64 MiB


// create the bytes in [length][body] format for the manifest
func WriteManifest(w io.Writer, m *Manifest) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("blob: encode manifest: %w", err)
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body))) // PutUint32 writes len of body in prefix
	if _, err := w.Write(prefix[:]); err != nil {
		return fmt.Errorf("blob: write manifest length: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("blob: write manifest body: %w", err)
	}
	return nil
}



// ReadManifest reads the bytes and generate the manifest from those
func ReadManifest(r io.Reader) (*Manifest, error) {
    // io.Reader is an interface implemented by types that provide a stream
    // of bytes, such as *os.File and net.Conn.
    
    // Return a pointer to the Manifest we construct below.
    var prefix [4]byte // 4-byte array: [00][00][00][00]

    /*
    Read() vs ReadFull()

    Read() may return fewer bytes than requested. With TCP, for example,
    we might ask for 4 bytes but receive only 2 in one Read() call.

    ReadFull() keeps reading until the buffer is completely filled
    (4 bytes here) or an error occurs.
    */
    if _, err := io.ReadFull(r, prefix[:]); err != nil {
        return nil, fmt.Errorf("blob: manifest length %w", err)
    }

    // Interpret the 4 bytes in prefix as a big-endian uint32.
    n := binary.BigEndian.Uint32(prefix[:])

    if n > maxManifestSize {
        return nil, fmt.Errorf(
            "blob: manifest length %d exceeds max %d",
            n,
            maxManifestSize,
        )
    }

    // Allocate a byte slice containing n bytes.
    body := make([]byte, n)

    // Read exactly n bytes into body.
    if _, err := io.ReadFull(r, body); err != nil {
        return nil, fmt.Errorf("blob: read manifest body: %w", err)
    }

    var m Manifest

    // Decode the JSON bytes in body into the Manifest struct.
    if err := json.Unmarshal(body, &m); err != nil {
        return nil, fmt.Errorf("blob: decode manifest: %w", err)
    }

    return &m, nil
}