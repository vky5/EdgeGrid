// Package blob moves large files between nodes in hashed chunks, without caring what's in them.
// the chunk hashes are built once from the file and sent before any chunk, so each chunk is checked against what was promised, not a hash sent with it
// why it's built this way: docs/blob-transfer.md
package blob
