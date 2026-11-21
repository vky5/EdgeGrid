---
id: 20261122-task-check-schedule
title: Checks on a sent task start at 10 minutes, double up to a day, and give up after a week unanswered
level: detail
system: tasks
status: accepted
date: 2026-11-22 00:57
tags: []
realizes: [20261122-task-sender-collects-results]
supersedes: []
---
## Decision
The next check is the claim time plus `checkWait(attempts)`: 10 minutes doubled per attempt, at most a day (node/task_recover.go). What a check does with the answer decides whether the task ends.

## Example
1. `slow` sent at 09:00. The row's first check is set to 09:10 (`FirstCheckAfter`).
2. Checks then come 20 min, 40 min, 80 min apart, and so on, reaching once a day after about 2 days.
3. bravo answers running: the row is left alone, however old the task is.
4. bravo answers done, failed or lost: the row stores it and is finished.
5. bravo unreachable and the task under 7 days old: left for its next check.
6. Unreachable and 7 days old: bravo is dialled once even if Tailscale shows it offline, then the row becomes lost, "no answer from bravo".
7. A shutting down mid-check never gives up on a task.

## Why
- Early checks catch quick tasks; later ones cost little when bravo is off.
- The next check is stored as a date, so a restart or a week off causes one check, not a burst.
- 7 days matches how long bravo keeps results (`task.ResultRetention`).

## Alternatives rejected
- **Fixed checks at 10 min, 1 hour, 1 day, 5 days**: stops at day 5 though bravo keeps results for 7.
- **Times 6 each check**: fewer checks, but a task ending at hour 2 is noticed at hour 7.
- **Trusting Tailscale's offline flag at give-up time**: Tailscale was still showing a stopped node online 40 s later, and can lag the other way too.

## Diagram
```d2
direction: right
ask: Ask bravo
run: Running, keep checking
fin: Done, failed or lost
young: Unreachable, keep checking
lost: Lost, no answer {style.stroke-dash: 4}

ask -> run: running
ask -> fin: finished
ask -> young: under 7 days
ask -> lost: 7 days old
```
