package node

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// spaceHeadroom is the free space to keep spare beyond the file itself (a guess).
const spaceHeadroom uint64 = 256 << 20

// freeBytesFunc is a variable so tests can fake a full disk.
var freeBytesFunc = freeBytes

// checkSpace refuses a blob that won't fit in the inbox's filesystem, so the sender hears so up front.
// Best-effort: an unreadable free-space figure allows the transfer, and nothing is reserved.
func (a *Node) checkSpace(label string, size int64) error {
	dir := filepath.Join(a.cfg.DataDir, "inbox")
	if err := os.MkdirAll(dir, 0o700); err != nil { // it may not exist yet
		log.Printf("blob: inbox %s: %v", dir, err)
		return errors.New("this node could not store the file")
	}

	free, err := freeBytesFunc(dir)
	if err != nil {
		log.Printf("blob: could not read free space in %s, not checking: %v", dir, err)
		return nil
	}

	need := uint64(size) + spaceHeadroom // size is non-negative: Manifest.Validate ran first
	if free < need {
		log.Printf("blob: refused %s: needs %s, %s free", label, formatBytes(need), formatBytes(free))
		return fmt.Errorf("not enough free space on this node: need %s (file %s plus %s spare), have %s",
			formatBytes(need), formatBytes(uint64(size)), formatBytes(spaceHeadroom), formatBytes(free))
	}
	return nil
}

func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
