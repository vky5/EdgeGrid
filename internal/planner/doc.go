// Package planner turns a request into a plan and dispatches it, one strategy per kind of plan.
// today the only case is a direct transfer from one node to another, which needs no choice at all
// it exists for 3+ nodes, where a peer can pull from several sources and must pick one by load and locality, like Dragonfly's scheduler, so expect the strategy interface to change then
package planner
