---
id: 20261122-task-result-query
title: Result requests are answered from the row, with lost and "no record of this task" for anything not yours
level: detail
system: tasks
status: accepted
date: 2026-11-22 01:05
tags: [acl]
realizes: [20261011-task-submit-to-stored-result]
supersedes: [20261011-task-result-query]
---
## Decision
`receiveResult` (node/task_result.go) answers one request per connection. A missing task and someone else's task get the same answer: lost, "no record of this task" (`task.NoRecord`).

## Example
1. A's result hello reaches `handlePeer` (node/peer.go), which routes it to `receiveResult` with A's identity from Tailscale WhoIs.
2. `ServeResult` (task/result.go) reads `{"task_id":"..."}`. A malformed ID is answered lost, "no record of this task", without a lookup.
3. `lookupResult` calls `ReceivedTaskFor(taskID, requester)` (db/tasks.go), which matches only the node that sent the task.
4. No database, no requester identity, a missing row, another node's row, or a read error: lost, "no record of this task".
5. Otherwise bravo returns the row as stored: running, done with output, failed or lost with its reason.
6. The answer goes out as result message version 2. A node still on version 1 gets "unsupported version".

## Why
- One answer for missing and foreign IDs stops probing for task IDs.
- Row statuses and answer statuses are the same four values, so a row is sent as it is.

## Alternatives rejected
- **A distinct "not yours" answer**: leaks which task IDs exist.
- **A separate "unknown" status**: for the sender it means the same as lost.

## Diagram
```d2
direction: down
req: Request with task_id
id: Valid ID?
ready: Database and sender known?
row: Row for this sender?
send: Send stored status
none: Lost, no record {style.stroke-dash: 4}

req -> id
id -> ready: yes
ready -> row: yes
row -> send: yes
id -> none: no
ready -> none: no
row -> none: no or error
```
