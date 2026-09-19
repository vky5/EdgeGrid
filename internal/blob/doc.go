// Package blob moves large, opaque byte sequences between nodes —
// chunked, hashed, resumable — without any opinion about what those bytes
// mean. A file, a dataset, a bundle of task output: to this package
// they're all just a Manifest and a list of chunks.
//
// The split from internal/executor is deliberate: executor knows about
// files on disk and turns them into blobs; this package only knows how to
// move a blob once it exists. Anything else that later needs to move a
// large payload between nodes (task output, say) uses this package
// directly instead of going through executor, which is artifact-specific.
// See docs/peer-discovery.md's "explicitly deferred" section for the
// task-dispatch work this is meant to support once it exists.
//
// Why a manifest, not a hash traveling with each chunk: TCP and WireGuard
// already guarantee a chunk arrives exactly as sent — a hash bundled with
// the chunk from the same sender can't add anything to that. What it can't
// guarantee is that what was sent is actually correct (the sender's own
// disk could be corrupted, for instance). A Manifest, built once from the
// original data and shared before any chunk moves, is what makes "does
// this match what was promised" a real, independent question instead of
// the sender grading its own homework. See OCI's image-manifest/blob-digest
// model — the design this borrows from.
package blob
