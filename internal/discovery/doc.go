// Package discovery is how EdgeGrid nodes find each other and say hello over the tailnet, with no central server.
// design: docs/peer-discovery.md
//
// the intent in the hello decides what follows it:
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
// file transfer: internal/blob and docs/blob-transfer.md
package discovery

// the Tailscale tag that marks a device as an EdgeGrid node, so a phone on the same tailnet isn't a peer
// fixed, not a setting, since every node must agree and a joining node only gets a join key
const Tag = "tag:edgegrid"
