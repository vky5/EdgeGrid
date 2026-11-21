---
id: 20261122-task-check-feeder-workers
title: One feeder claims due sent tasks and hands them to a pool of workers over a channel
level: detail
system: tasks
status: accepted
date: 2026-11-22 00:56
tags: []
realizes: [20261122-task-sender-collects-results]
supersedes: []
---
## Decision
`Start` (node/node.go) launches one feeder and `TASK_CHECK_WORKERS` workers, default 5, joined by an unbuffered channel (node/task_checks.go). The feeder claims each due task before handing it over.

## Example
1. At startup, then every minute (`checkEvery`), the feeder calls `DueSentTasks(now, 100)` (db/tasks.go). Nothing due: no further work.
2. 12 tasks are due. It calls `currentPeers` once, Tailscale's device list by ID. If that fails, it skips the round.
3. For each task, `ClaimSentTask` adds one attempt and moves the next check to now plus `checkWait`. A failed claim means it is done or another process took it.
4. It sends the task on the channel. 5 go out; the 6th waits until a worker frees up.
5. Each worker runs `checkSentTask` (node/task_recover.go) and takes the next.
6. On shutdown the feeder exits and closes the channel, the workers stop, and `Close` waits for all 6 on `a.runs` before closing the database.

## Why
- The claim stops the next minute's look from handing out a task still queued or being asked.
- Two processes on one profile can't both claim the same check.
- The channel holds nothing, so a crash never leaves claimed tasks unasked.

## Alternatives rejected
- **A list in memory of handed-out tasks**: two processes on one profile would both ask.
- **Batches of 5**: one dead peer holds up the other four.

## Diagram
```d2
direction: down
tick: Every minute
due: DueSentTasks
peers: currentPeers
claim: ClaimSentTask
chan: Jobs channel
w: 5 workers
check: checkSentTask
skip: Skip round {style.stroke-dash: 4}

tick -> due
due -> peers: some due
peers -> claim: list ok
peers -> skip: no list
claim -> chan: claimed
chan -> w
w -> check
```
