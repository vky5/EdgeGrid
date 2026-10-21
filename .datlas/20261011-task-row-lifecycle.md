---
id: 20261011-task-row-lifecycle
title: One tasks table row per task, pruned after 7 days and reconciled on startup
level: detail
system: tasks
status: accepted
date: 2026-10-11 00:14
tags: []
realizes: [20261011-task-submit-to-stored-result]
supersedes: []
---
## Decision
Task state lives in the SQLite `tasks` table (schema version 2, db/migrate.go), one row per task per end, with `role` set to sent or received.

## Example
1. `RecordTaskStart` inserts a running row with task ID, role, peer ID, hostname, kind and start time.
2. `RecordTaskFinish` sets the final status. Output is kept only for done.
3. After each accepted task, `PruneTasks` deletes finished rows that started over 7 days ago (`taskRetention`). Running rows are never pruned.
4. On startup, `ReconcileStaleTasks` (called from node.go) marks every running row interrupted, "node restarted".
5. If the history database failed to open, reconcile is skipped with a warning and tasks are refused.
6. Task reconcile runs only after `ReconcileStaleTransfers` succeeds, so a transfer reconcile error also skips it and leaves stale running task rows.

## Why
- A crash would otherwise leave a task looking like it is running forever.
- Retention bounds table growth while giving the sender time to collect.

## Known gap
- node.go chains the two reconciles with `else if`, so a failed `ReconcileStaleTransfers` skips `ReconcileStaleTasks`.
- Task rows left running then stay running until a later restart succeeds. The fix is to run the two reconciles independently.

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
