package node

import (
	"errors"
	"log"
	"path/filepath"
)

// the folder that says which peers may send this node tasks, laid out like acl, e.g. <DataDir>/acl-tasks/<StableID>
// kept apart from the file ACL, since trusting a peer with files says nothing about tasks
const taskACLDir = "acl-tasks"

// the rule for peers with no file: "allow", or anything else, which means deny
const taskACLDefaultFile = "acl-tasks.default"

// who may send this node tasks
var taskACL = aclKind{
	dir:         taskACLDir,
	defaultFile: taskACLDefaultFile,
}

func taskACLLookup(dataDir, stableID string) (ACLEntry, bool, error) {
	return taskACL.lookup(dataDir, stableID)
}

func taskACLSet(dataDir, stableID, hostname string, allow bool) error {
	return taskACL.set(dataDir, stableID, hostname, allow)
}

func taskACLList(dataDir string) map[string]ACLEntry { return taskACL.list(dataDir) }

func taskACLDefaultAllows(dataDir string) bool { return taskACL.defaultAllows(dataDir) }

// every saved task decision, peer ID to allowed
// a peer with no file is missing from the map, not false
func (a *Node) TrustedTaskPeers() map[string]bool {
	out := map[string]bool{}
	for id, e := range taskACLList(a.cfg.DataDir) {
		out[id] = e.Allow
	}
	return out
}

// saves whether a peer may send this node tasks; the hostname is only for display
func (a *Node) SetTaskTrust(stableID, hostname string, allow bool) error {
	return taskACL.set(a.cfg.DataDir, stableID, hostname, allow)
}

// refuses a task from a peer the task ACL doesn't allow, with "this node is not accepting tasks from you"
// there is no CPU or memory check yet
//
//	acl-tasks/nK7xQ2CNTRL says allow=true                        → nil
//	acl/nK7xQ2CNTRL says allow=true, acl-tasks has no file       → refused; file trust doesn't count
//	no file, acl-tasks.default says "allow"                      → nil
//	no file, acl-tasks.default missing or says "Allow"           → refused
//	the peer's hello claims a trusted node_id                    → ignored; only Tailscale's ID is checked
func (a *Node) checkTaskAccept(stableID, label string) error {
	dataDir := a.cfg.DataDir

	allowed := taskACLDefaultAllows(dataDir)
	entry, found, err := taskACLLookup(dataDir, stableID)
	switch {
	case err != nil:
		// a file we can't read counts as deny
		log.Printf("task: refusing %s: acl lookup for %q: %v", label, stableID, err)
		allowed = false
	case found:
		allowed = entry.Allow
	}
	if !allowed {
		log.Printf("task: refused %s (%s) — allow them with SetTaskTrust, or create %s",
			label, stableID, filepath.Join(dataDir, taskACLDir, stableID))
		return errors.New("this node is not accepting tasks from you")
	}
	return nil
}
