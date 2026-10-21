---
id: 20261013-task-send-to-shown-result
title: Sending a task waits on the executing node and shows its output to the sender
level: story
system: tasks
status: proposed
date: 2026-10-13 22:40
tags: []
realizes: []
supersedes: [20261011-task-sender-await-result]
---
## Decision
After a peer accepts a task, the sender keeps asking that peer how it went and shows the output or error when it ends. Stopping the wait never stops the task.

## Example
1. On node A you run `edgegrid task send hello`, or press `x` on bravo in the Peers tab and type `hello`.
2. A offers the task to each online peer in turn. Bravo accepts, and A notes the task as running.
3. Bravo runs `hello` in the background and stores how it ended.
4. A asks bravo after 1 second, then 2, 4, 8, then every 10 seconds.
5. Bravo answers running until the run ends, then done with `hello from bravo`.
6. A stores the outcome on its own record. The CLI prints the output, and the Peers tab shows it in its status line.
7. Ctrl+C or `esc` stops A asking. Bravo keeps running and keeps the result for 7 days.
8. After 10 minutes without an end, A gives up and says the task may still be running on bravo.

## Why
- Bravo never has to dial back to A.
- A lost poll is a network problem, not a task failure, so it is retried.

## Alternatives rejected
- **Bravo pushes the result to A**: not explored; polling reuses the result request bravo already serves.
- **Return to the peer list right after accept**: the sender would never see the output.

## Diagram
```d2
shape: sequence_diagram
you: You
A: Node A
B: Node bravo

send: Send {
  you -> A: send hello
  A -> B: offer task
  B -> A: accepted
  A -> you: waiting for result
}
wait: Wait {
  A -> B: how did it go
  B -> A: running
  A -> B: ask again later
  B -> A: done with output
  A -> you: show output
}
```
