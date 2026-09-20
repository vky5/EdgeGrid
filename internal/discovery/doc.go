// Package discovery is how EdgeGrid nodes find each other and exchange
// state over the tailnet — no coordinator, no gossip. See
// docs/peer-discovery.md for the design.
//
// A connection's intent decides what follows the hello:
//
//	Node A (dialer)                        Node B (listener)
//	  │                                      │
//	  │ Start()                              │ Start()
//	  │ Snapshot() — who's online            │ server.Serve() — accepting
//	  │                                      │
//	  │ dialAndGreet() / SendBlob()          │
//	  │── Dial() ───────────────────────────>│ handle()
//	  │                                      │ IdentifyPeer() — WhoIs
//	  │                                      │
//	  │── hello {intent} ───────────────────>│ ExchangeAsListener()
//	  │<──────────────────── hello {reply} ──│
//	  │                                      │
//	  │                                      │ OnPeer → handlePeer()
//	  │                                      │ switch hello.Intent
//	  │                                      │
//	  │                       intent=hello:  │   close
//	  │                       intent=blob:   │   keep reading ↓
//	  │                                      │
//	  │── manifest ─────────────────────────>│ Validate(), ACL, size cap
//	  │   (file size, chunk hashes)          │
//	  │<──────────── verdict accept/refuse ──│
//	  │                                      │
//	  │── chunk 0 ──────────────────────────>│
//	  │── chunk 1 ──────────────────────────>│
//	  │── chunk 2 ──────────────────────────>│
//	  │           ...                        │
//
// See internal/blob and docs/blob-transfer.md.
package discovery

// Tag is the Tailscale ACL tag marking a device as an EdgeGrid node, so a
// laptop or phone sharing the tailnet isn't treated as a peer. Fixed rather
// than configurable: every node needs to agree on it, and a joining node
// only ever gets a join key, never the issuer's settings.
const Tag = "tag:edgegrid"
