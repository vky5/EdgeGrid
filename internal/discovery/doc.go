// Package discovery is how EdgeGrid nodes find each other and exchange
// state over the tailnet — no coordinator, no gossip. See
// docs/peer-discovery.md for the design.
package discovery

// Tag is the Tailscale ACL tag marking a device as an EdgeGrid node, so a
// laptop or phone sharing the tailnet isn't treated as a peer. Fixed rather
// than configurable: every node needs to agree on it, and a joining node
// only ever gets a join key, never the issuer's settings.
const Tag = "tag:edgegrid"
