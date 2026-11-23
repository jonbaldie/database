# TableSchema

TLC reached 30 states and 46 steps. The chart draws 13 states and 18 arrows.
The chart does not draw 5 steps that stay in one chart state.

## SchemaSuccessHasDurableImage breaks
The shortest path has 5 steps:
0. Create absent table
1. Change in-memory table shape: memory changed
2. Write metadata temporary file: temporary written
3. Sync metadata file: temporary synced
4. Install metadata file: metadata renamed
5. Sync metadata directory: Success returned without persisted shape

## Rules that hold
NoDeadlock
