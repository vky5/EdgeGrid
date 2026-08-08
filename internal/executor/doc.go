// Package executor holds the implementations that actually move or process
// data once a plan has chosen what to do.
//
// Nothing lives here yet. The first executor will be artifact transfer:
// chunk a set of files, hash each chunk, stream them to a peer over the
// tailnet, and resume from the last verified chunk after an interruption.
package executor
