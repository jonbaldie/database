# TableSchema

TLC reached 34 states and 50 steps. The chart draws 12 states and 18 arrows.
The chart does not draw 9 steps that stay in one chart state.

## SchemaSuccessHasDurableImage breaks
The shortest path has 25 steps:
0. Create absent table
1. Change in-memory table shape: memory changed
2. Write metadata temporary file: temporary written
3. Sync metadata file: temporary synced
4. Install metadata file: metadata renamed
5. Sync metadata directory: Table change persisted
6. Begin next table change: Reshape existing table
7. Change in-memory table shape: memory changed
8. Write metadata temporary file: temporary written
9. Sync metadata file: temporary synced
10. Install metadata file: metadata renamed
11. Sync metadata directory: Table change persisted
12. Begin next table change: Drop table
13. Change in-memory table shape: memory changed
14. Write metadata temporary file: temporary written
15. Sync metadata file: temporary synced
16. Install metadata file: metadata renamed
17. Sync metadata directory: drop checkpoint
18. Checkpoint after removing table: Table change persisted
19. Begin next table change: Recreate table name
20. Change in-memory table shape: memory changed
21. Write metadata temporary file: temporary written
22. Sync metadata file: temporary synced
23. Install metadata file: metadata renamed
24. Sync metadata directory: Table change persisted
25. Restart after recreate: Dropped rows reappeared

## Rules that hold
NoDeadlock
