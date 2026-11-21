package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// a file that's an exact multiple of the chunk size splits into exactly that many chunks
// e.g. 16 bytes at size 4 gives 4 chunks, not 5
func TestBuildManifestExactMultipleOfChunkSize(t *testing.T) {
	content := "AAAABBBBCCCCDDDD" // 16 bytes
	path := writeTempFile(t, content)

	m, err := BuildManifest(path, "text/plain", 4)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	if m.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", m.Size, len(content))
	}
	if m.SHA256 != sha256Hex(content) {
		t.Errorf("whole-blob SHA256 mismatch")
	}
	if len(m.Chunks) != 4 {
		t.Fatalf("got %d chunks, want 4", len(m.Chunks))
	}

	wantChunks := []string{"AAAA", "BBBB", "CCCC", "DDDD"}
	for i, want := range wantChunks {
		c := m.Chunks[i]
		if c.Index != i {
			t.Errorf("chunk %d: Index = %d", i, c.Index)
		}
		if c.Offset != int64(i*4) {
			t.Errorf("chunk %d: Offset = %d, want %d", i, c.Offset, i*4)
		}
		if c.Length != 4 {
			t.Errorf("chunk %d: Length = %d, want 4", i, c.Length)
		}
		if c.SHA256 != sha256Hex(want) {
			t.Errorf("chunk %d: hash mismatch for %q", i, want)
		}
	}
}

// a file that doesn't divide evenly ends with one shorter chunk, not an error or a lost piece
// e.g. 10 bytes at size 4 gives 4, 4, 2
func TestBuildManifestShorterFinalChunk(t *testing.T) {
	content := "AAAABBBBCC" // 10 bytes, chunkSize 4 -> 4,4,2
	path := writeTempFile(t, content)

	m, err := BuildManifest(path, "text/plain", 4)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if len(m.Chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(m.Chunks))
	}
	last := m.Chunks[2]
	if last.Length != 2 {
		t.Errorf("final chunk Length = %d, want 2", last.Length)
	}
	if last.SHA256 != sha256Hex("CC") {
		t.Errorf("final chunk hash mismatch")
	}
}

func TestBuildManifestEmptyFile(t *testing.T) {
	path := writeTempFile(t, "")

	m, err := BuildManifest(path, "text/plain", 4)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if m.Size != 0 {
		t.Errorf("Size = %d, want 0", m.Size)
	}
	if len(m.Chunks) != 0 {
		t.Errorf("got %d chunks, want 0", len(m.Chunks))
	}
	if m.SHA256 != sha256Hex("") {
		t.Errorf("empty-file SHA256 should still be sha256(\"\")")
	}
}

// a chunk size of 0 or less uses the default, so a small file becomes one chunk
func TestBuildManifestDefaultsChunkSize(t *testing.T) {
	content := strings.Repeat("x", 100)
	path := writeTempFile(t, content)

	m, err := BuildManifest(path, "text/plain", 0)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if len(m.Chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (file is far smaller than DefaultChunkSize)", len(m.Chunks))
	}
	if m.Chunks[0].Length != len(content) {
		t.Errorf("chunk Length = %d, want %d", m.Chunks[0].Length, len(content))
	}
}

func TestBuildManifestMissingFile(t *testing.T) {
	_, err := BuildManifest("/does/not/exist", "text/plain", 4)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
