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

// the longest any incoming transfer may run, 30 minutes, even while chunks keep arriving
const blobReceiveTimeout = 30 * time.Minute

// saves a file a peer sends into the inbox, if this node allows it, e.g. ~/.edgegrid/<profile>/inbox/a.txt
// every check happens before the verdict, so a refusal reaches the sender as a reason, not a dropped connection
func (a *Node) receiveBlob(who *apitype.WhoIsResponse, hello discovery.Hello, conn net.Conn) {
	began := time.Now()
	if err := conn.SetDeadline(began.Add(verdictTimeout)); err != nil {
		log.Printf("blob: set deadline: %v", err)
		return
	}

	// who the peer is comes from Tailscale, never from the ID in its hello
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
		// the filename comes from the peer, so it only ever becomes one plain file name in the inbox
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

	// time the transfer and the final check separately, so a slow network can be told from a slow disk
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

// makes an empty file in the inbox and returns its path, e.g. "a.txt" becomes "a (1).txt" if taken
// creating the file is the reservation, so two transfers with the same name never share a path
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
