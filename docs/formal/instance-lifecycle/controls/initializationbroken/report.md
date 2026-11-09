# Initialization

TLC reached 15 states and 25 steps. The chart draws 13 states and 20 arrows.
The chart does not draw 5 steps that stay in one chart state.

## FailureRemovesOwnedCatalog breaks
The shortest path has 8 steps:
0. empty
1. Claim the empty directory: claimed
2. Write staged catalog: catalog staged
3. Write staged metadata: both staged
4. Install catalog link: catalog linked
5. Remove staged catalog: catalog installed
6. Metadata destination already exists: install failed
7. Remove this attempt's catalog: cleaned
8. Discard initialization claim: failed

## Rules that hold
SuccessHasBothFiles, CrashRetainsClaim, MetadataNeedsCatalog, NoDeadlock
