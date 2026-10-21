---
id: 20261011-task-accept-gate
title: Accept runs ordered checks and records the row before the verdict
level: detail
system: tasks
status: accepted
date: 2026-10-11 00:11
tags: [acl]
realizes: [20261011-task-submit-to-stored-result]
supersedes: []
---
## Decision
The `accept` closure inside `receiveTask` (node/task_receive.go) is the only place a task is admitted. The first failing check refuses with a reason sent to A.

## Example
1. `receiveTask` takes A's `stableID` from Tailscale WhoIs, never from the hello's `NodeID`.
2. `checkTaskAccept` (node/task_acl.go) reads A's task ACL entry, else the default policy. An unreadable entry denies.
3. `executor.Supports(kind)` must be true.
4. `a.history` must exist, else "this node can't record results".
5. `taskSlot.TryClaim` (node/task_slot.go) admits one task at a time.
6. `RecordTaskStart(RoleReceived, ...)` writes the running row. If it fails, the slot is released and the task refused.
7. If `ProcessTask` then fails to write the accept verdict, the slot is released and `finishTask` marks the row failed, "verdict not delivered".

## Why
- The task ACL is separate from the file ACL, so trusting one implies nothing about the other.
- Writing the row before the verdict means a result request never races the insert.

## Alternatives rejected
- **Accept without a database**: the result could never be reported back.

## Diagram
```d2
direction: down

offer: Offer from A
acl: ACL allows A?
kind: Kind supported?
db: Database present?
slot: Slot free?
row: Insert running row
send: Write accept verdict
run: Start background run
refuse: Refuse with reason {style.stroke-dash: 4}
failed: Release slot, row failed {style.stroke-dash: 4}

offer -> acl
acl -> kind: yes
kind -> db: yes
db -> slot: yes
slot -> row: yes
row -> send: ok
send -> run: ok

acl -> refuse: no
kind -> refuse: no
db -> refuse: no
slot -> refuse: busy
row -> refuse: write failed
send -> failed: write failed
```
