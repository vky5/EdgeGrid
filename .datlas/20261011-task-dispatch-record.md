---
id: 20261011-task-dispatch-record
title: DispatchTask tries peers in order and records a sent row only on accept
level: detail
system: tasks
status: proposed
date: 2026-10-11 00:31
tags: []
realizes: [20261013-task-send-to-shown-result]
supersedes: []
---
## Decision
`DispatchTask` (node/task_dispatch.go) now returns the accepting peer, and `recordSentTask` writes a sent row for it. Recording is best-effort and never fails the dispatch.

## Example
1. Peers `[B, C]`. For each, `DispatchTask` dials, exchanges a task hello, then calls `task.Offer`.
2. B is unreachable: the error is collected and C is tried.
3. C refuses with "already running a task": collected, and no peers are left.
4. Result: `errors.Join` of all reasons plus "no peer accepted the task", and no row is written.
5. On a later try C accepts: `recordSentTask` calls `RecordTaskStart(RoleSent, t1, C.ID, C.Hostname, kind)` and `DispatchTask` returns C.
6. If A has no database or the insert fails, it only logs. The task still runs on C, untracked on A.

## Why
- The row is written after accept, so refused tasks leave no rows.
- `peer.ID` is the Tailscale stable ID, the same identity B checks on a result request.

## Alternatives rejected
- **Fail the dispatch when the row write fails**: C is already running the task, so failing would lie.

## Diagram
```d2
direction: down

next: Next peer in list
dial: Dial and hello ok?
offer: Offer accepted?
row: recordSentTask writes sent row
ret: Return accepting peer
skip: Collect error
none: No peer accepted {style.stroke-dash: 4}

next -> dial
dial -> offer: yes
offer -> row: yes
row -> ret
dial -> skip: no
offer -> skip: no
skip -> next: peers left
skip -> none: list empty
```
