// Package planner turns a request into a plan and dispatches it.
//
// The intent is one strategy per kind of plan, so that adding a new way to
// move data is adding a strategy rather than editing the caller. Today there
// is exactly one case worth planning for — a direct transfer from one node to
// another — which needs no placement decision at all.
//
// The second strategy is the reason this package exists: with three or more
// nodes, a peer can pull from more than one source, and choosing the source
// becomes a real decision (see Dragonfly's scheduler, which assigns each peer
// a parent based on load and locality). Expect the strategy interface defined
// for the first case to need reshaping when that lands — a direct transfer
// never has to ask questions that peer-assisted transfer must answer.
package planner
