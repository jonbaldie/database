# CatalogPublication

TLC reached 15 states and 19 steps. The chart draws 12 states and 14 arrows.
The chart does not draw 4 steps that stay in one chart state.

## OutcomeMatchesPublication breaks
The shortest path has 4 steps:
0. queued
1. Collect queued requests: batch collected
2. Apply isolated request changes: isolated changes applied
3. Validation or persistence fails: failed
4. Notify failed waiters: Success announced without durable publication

## Rules that hold
NoDeadlock
