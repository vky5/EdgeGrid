---
id: 20261011-task-result-query
title: Result requests are answered from the row, with unknown for anything not yours
level: detail
system: tasks
status: accepted
date: 2026-10-11 00:13
tags: [acl]
realizes: [20261011-task-submit-to-stored-result]
supersedes: []
---
## Decision
`receiveResult` (node/task_result.go) answers one request per connection and never reveals whether a task ID exists for someone else.

## Example
1. A's result hello reaches `handlePeer` (node/peer.go), which routes the result intent to `receiveResult`.
2. `receiveResult` takes A's `stableID` from Tailscale and sets a 10 second deadline (`resultTimeout`).
3. `ServeResult` (task/result.go) reads the request. A non-uuid ID is answered unknown without a lookup.
4. `lookupResult` calls `ReceivedTaskFor(taskID, requester)` (db/tasks.go), which matches both the task ID and the recorded dispatcher.
5. No database, no requester identity, a missing row, another peer's row or a read error all answer unknown. Only a real read error is logged.
6. Otherwise B returns running, done, failed or interrupted, with output or error.

## Why
- One shared answer for missing and foreign IDs stops probing.
- `Finished()` lets the sender stop polling on anything except running.

## Alternatives rejected
- **A distinct "not yours" status**: leaks which task IDs exist.

## Diagram
```d2
direction: down

req: Request with task_id
uuid: Valid uuid?
ready: Database and requester known?
lookup: ReceivedTaskFor ID and A
found: Row found for A?
status: Stored status, output, error
unknown: unknown {style.stroke-dash: 4}

req -> uuid
uuid -> ready: yes
ready -> lookup: yes
lookup -> found
found -> status: yes
uuid -> unknown: no
ready -> unknown: no
found -> unknown: no or error
```
