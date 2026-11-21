---
id: 20261122-task-row-lifecycle
title: Task rows end as done, failed or lost, sent rows carry a next check, and restart only touches received rows
level: detail
system: tasks
status: accepted
date: 2026-11-22 00:58
tags: []
realizes: [20261011-task-submit-to-stored-result, 20261122-task-sender-collects-results]
supersedes: [20261013-task-row-lifecycle, 20261011-task-sent-row-outcome]
---
## Decision
Task rows (db/tasks.go) use four statuses: running, done, failed, lost. Sent rows also store `attempts` and `next_check_at`. Startup marks only received running rows lost.

## Example
1. bravo's received row for `slow`: running, then done, failed, or lost "node restarted" if bravo restarts mid-run.
2. A's sent row: running, attempts 0, next check 09:10.
3. A claims it at 09:10: attempts 1, next check 09:30.
4. bravo answers done: A's row becomes done with bravo's output.
5. bravo answers lost "no record of this task": A stores lost with that reason, not failed.
6. A restarts while the row is running: the row stays running.
7. Finished rows are still pruned after 7 days, and only when a task is accepted, so a node that only sends never prunes.

## Why
- Failed means the task broke, so resending won't help; lost means no result through no fault of the task.
- Restarting A doesn't stop work on bravo, so its sent rows can't be marked lost.

## Alternatives rejected
- **Keep interrupted and unknown**: two more values meaning the same as lost.
- **Store "no record" as failed**: mixes bravo forgetting with the task crashing.

## Diagram
```d2
direction: right
running: running
done: done
failed: failed
lost: lost
gone: pruned {style.stroke-dash: 4}

running -> done: finished
running -> failed: task broke
running -> lost: restart or no answer
done -> gone: after 7 days
failed -> gone: after 7 days
lost -> gone: after 7 days
```
