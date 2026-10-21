---
id: 20261013-task-row-lifecycle
title: One tasks row per task per end, pruned after 7 days, with startup reconciles that run independently
level: detail
system: tasks
status: proposed
date: 2026-10-13 22:43
tags: []
realizes: [20261011-task-submit-to-stored-result]
supersedes: [20261011-task-row-lifecycle]
---
## Decision
Task state lives in the SQLite `tasks` table (schema version 2, db/migrate.go), one row per task per end, with `role` set to sent or received (db/tasks.go).

## Example
1. `RecordTaskStart` inserts a running row: task ID, role, peer ID, hostname, kind, start time. B writes it in `accept`, A in `recordSentTask`.
2. `RecordTaskFinish` sets the final status. Output is kept only for done, and done over 64KB becomes failed.
3. After each accepted task, `PruneTasks` deletes finished rows started over 7 days ago (`taskRetention`). Running rows are never pruned.
4. On startup node.go runs `ReconcileStaleTransfers` and `ReconcileStaleTasks` separately, so one failing no longer skips the other.
5. `ReconcileStaleTasks` marks every running row interrupted, "node restarted". It ignores role, so A's sent rows are marked too, even if B finished.
6. Gap: a node that only sends never prunes, because pruning only runs on an accepted task.
7. Gap (unverified): `edgegrid task send` starts its own node and reconciles. Sharing a data dir with a running dashboard could mark that node's live rows interrupted.

## Why
- A crash would otherwise leave a task looking like it is running forever.
- Retention bounds table growth while giving the sender time to collect.

## Alternatives rejected
- **Never prune**: the table grows without bound.

## Diagram
```d2
direction: right

running: running
done: done
failed: failed
intr: interrupted
gone: deleted {style.stroke-dash: 4}

running -> done: run succeeded
running -> failed: error or too large
running -> intr: shutdown or restart
done -> gone: pruned after 7 days
failed -> gone: pruned after 7 days
intr -> gone: pruned after 7 days
```
