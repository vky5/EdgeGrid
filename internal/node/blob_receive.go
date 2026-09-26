package node

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"tailscale.com/client/tailscale/apitype"
)

// blobReceiveTimeout is the outer ceiling no inbound transfer may run
// past, even while idleTimeout keeps getting refreshed by steady progress.
const blobReceiveTimeout = 30 * time.Minute

//	takes an inbound blob into the profile's inbox, if this node's
//
// policy allows it. Every decision — ACL, size cap, where the file goes — is
// made inside the accept callback so that a refusal always reaches the sender
// as a verdict instead of a hung-up connection.
func (a *Node) receiveBlob(who *apitype.WhoIsResponse, hello discovery.Hello, conn net.Conn) {
	began := time.Now()
	if err := conn.SetDeadline(began.Add(verdictTimeout)); err != nil {
		log.Printf("blob: set deadline: %v", err)
		return
	}

	// Identity for the ACL is what Tailscale attributes, never
	// hello.NodeID that one is the peer's own claim.
	var stableID, label string
	if who != nil && who.Node != nil {
		stableID = string(who.Node.StableID)
		label = who.Node.ComputedName
	}
	if label == "" {
		label = hello.NodeID
	}

	dir := filepath.Join(a.cfg.DataDir, "inbox")
	var dest string
	var refused bool
	var historyID int64
	var haveHistory bool

	// ? This is the callback function responsible for checking the ACL
	accept := func(m *blob.Manifest) (string, error) {
		historyID, haveHistory = recordTransferStart(a.history, db.Inbound, stableID, label, m.Name, m.Size)

		if err := a.checkAccept(stableID, label, m); err != nil {
			refused = true
			return "", err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("blob: inbox %s: %v", dir, err)
			return "", errors.New("this node could not store the file")
		}
		// The filename is the peer's, so it only ever becomes a single
		// sanitised path component — see blob.SafeName.
		p, err := reserveInboxFile(dir, blob.SafeName(m.Name))
		if err != nil {
			log.Printf("blob: reserve in %s: %v", dir, err)
			return "", errors.New("this node could not store the file")
		}
		dest = p
		return p, nil
	}

	log.Printf("blob: inbound connection from %s", label)
	t := a.transfers.start(Inbound, label)

	// Split the time into the part that moved bytes and the part spent
	// flushing and re-hashing at the end, so a slow transfer can be told
	// apart from a slow disk. Receive runs on this goroutine, so plain
	// variables are safe here.
	var verifyBegan time.Time
	report := t.progressFunc()
	progress := func(p blob.Progress) {
		refreshDeadline(conn, began, idleTimeout, blobReceiveTimeout)
		if p.Verifying && verifyBegan.IsZero() {
			verifyBegan = time.Now()
		}
		report(p)
	}

	m, err := blob.Receive(conn, accept, progress)
	a.transfers.finish(t, err)

	sha256 := ""
	if err == nil {
		sha256 = m.SHA256
	}
	recordTransferFinish(a.history, historyID, haveHistory, err, refused, sha256)

	if err != nil {
		log.Printf("blob: receive from %s failed: %v", label, err)
		if dest != "" {
			if rmErr := os.Remove(dest); rmErr != nil {
				log.Printf("blob: could not remove partial %s: %v", dest, rmErr)
			}
		}
		return
	}

	end := time.Now()
	if verifyBegan.IsZero() {
		verifyBegan = end
	}
	moved, verified := verifyBegan.Sub(began), end.Sub(verifyBegan)
	log.Printf("blob: received %q, %d bytes in %d chunks from %s -> %s (sha256 %s)",
		m.Name, m.Size, len(m.Chunks), label, dest, m.SHA256)
	log.Printf("blob: transfer took %s (%.1f MB/s), then verify+flush %s",
		moved.Round(time.Millisecond), rateMBps(m.Size, moved), verified.Round(time.Millisecond))
}

// reserveInboxFile creates an empty file in dir named after name, adding
// " (1)", " (2)" and so on if it exists, and returns its path. O_EXCL makes
// the create the reservation, so two transfers landing the same name at once
// can't pick the same path.
func reserveInboxFile(dir, name string) (string, error) {
	if name == "" {
		name = "blob"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	for n := 0; n < 1000; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		path := filepath.Join(dir, candidate)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("too many files named %q in %s", name, dir)
}
