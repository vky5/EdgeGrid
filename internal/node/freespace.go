package node

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// free space to keep spare on top of the file, 256 MB (a guess)
const spaceHeadroom uint64 = 256 << 20

// reads free disk space; tests swap it to fake a full disk
var freeBytesFunc = freeBytes

// refuses a file that won't fit on the inbox's disk, so the sender hears why up front
// e.g. a 1 GiB file with 900 MiB free: "need 1.2 GiB (file 1.0 GiB plus 256.0 MiB spare), have 900.0 MiB"
// if free space can't be read the file is allowed, and no space is set aside
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
