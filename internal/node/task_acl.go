package node

import (
	"errors"
	"log"
	"path/filepath"
)

// The task ACL is separate from the file ACL — trusting one direction
// implies nothing about the other. Same shape as acl.go, see aclstore.go.
const taskACLDir = "acl-tasks"

// taskACLDefaultFile holds the policy for peers with no entry: "allow" or
// anything else, which means deny.
const taskACLDefaultFile = "acl-tasks.default"

// taskACL is the task ACL's storage — see aclstore.go for the shared
// mechanics, shared with the file ACL in acl.go.
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

// TrustedTaskPeers reports every recorded task decision as StableID ->
// allowed. Peers with no entry are absent, not false.
func (a *Node) TrustedTaskPeers() map[string]bool {
	out := map[string]bool{}
	for id, e := range taskACLList(a.cfg.DataDir) {
		out[id] = e.Allow
	}
	return out
}

// SetTaskTrust records whether stableID may dispatch tasks to this node.
// hostname is only for display — the entry is keyed on stableID.
func (a *Node) SetTaskTrust(stableID, hostname string, allow bool) error {
	return taskACL.set(a.cfg.DataDir, stableID, hostname, allow)
}

// checkTaskAccept applies this node's inbound task policy — mirrors
// checkAccept's ACL step. No resource check yet; that's still a no-op.
func (a *Node) checkTaskAccept(stableID, label string) error {
	dataDir := a.cfg.DataDir

	allowed := taskACLDefaultAllows(dataDir)
	entry, found, err := taskACLLookup(dataDir, stableID)
	switch {
	case err != nil:
		// Fail closed: an unreadable entry is not "no entry".
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
