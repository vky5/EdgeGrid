---
id: 20261011-task-sender-await-result
title: Sender records each dispatched task and polls the executing node until it finishes
level: story
system: tasks
status: proposed
date: 2026-10-11 00:30
tags: []
realizes: []
supersedes: []
---
## Decision
A writes a sent row when a peer accepts its task, then polls that peer for the result and writes the outcome onto the same row. This is uncommitted, and nothing calls the polling side yet.

## Example
1. `edgegrid task send` or the Peers tab builds task `t1` and calls `DispatchTask` (node/task_dispatch.go).
2. `DispatchTask` offers `t1` to each peer in order and stops at the first accept, say B.
3. `recordSentTask` writes a running sent row for `t1`, keyed to B's Tailscale ID.
4. `DispatchTask` returns B so the caller knows whom to ask. Today main.go discards it.
5. The caller would call `AwaitResult(B, t1)` (node/task_await.go). No caller exists yet.
6. `AwaitResult` polls B through `askResult`, retrying network failures, for up to 10 minutes.
7. When B reports a finished status, `recordSentResult` writes it onto A's sent row.
8. If B answers unknown, A records failed. If 10 minutes pass, A returns `ErrAwaitTimeout` and the row stays running.

## Why
- A learns the outcome without B having to dial back.
- A dropped poll is a network problem, not a task failure, so it is retried.

## Alternatives rejected
- **B pushes the result to A**: not explored in this work; polling reuses the result intent B already serves.

## Diagram
```d2
shape: sequence_diagram
cli: CLI or TUI
A: Node A
adb: A tasks table
B: Node B

dispatch: Dispatch {
  cli -> A: DispatchTask t1
  A -> B: offer t1
  B -> A: accept
  A -> adb: insert sent row
  A -> cli: accepted by B
}
poll: Poll, not wired yet {
  cli -> A: AwaitResult B, t1
  A -> B: ask t1
  B -> A: running
  A -> B: ask t1 after backoff
  B -> A: done with output
  A -> adb: set final status
  A -> cli: result
}
```
