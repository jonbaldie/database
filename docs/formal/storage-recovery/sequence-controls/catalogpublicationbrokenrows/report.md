# CatalogPublication

TLC reached 12 states and 16 steps. The chart draws 12 states and 14 arrows.
The chart does not draw 2 steps that stay in one chart state.

## OutcomeMatchesPublication breaks
The shortest path has 9 steps:
0. queued
1. Collect queued requests: batch collected
2. Apply isolated request changes: isolated changes applied
3. Validate proposed catalog: validated
4. Stage changed row image: row transaction staged
5. Commit and sync rows: rows synced
6. Checkpoint changed row schema: row schema checkpointed
7. Persist catalog schema: schema persisted
8. Publish catalog revision: revision published
9. Notify successful waiters: Success announced without durable publication

## Rules that hold
NoDeadlock
