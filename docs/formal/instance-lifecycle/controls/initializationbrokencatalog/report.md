# Initialization

TLC reached 14 states and 24 steps. The chart draws 13 states and 20 arrows.
The chart does not draw 4 steps that stay in one chart state.

## MetadataNeedsCatalog breaks
The shortest path has 6 steps:
0. empty
1. Claim the empty directory: claimed
2. Write staged catalog: catalog staged
3. Write staged metadata: both staged
4. Install catalog link: catalog linked
5. Remove staged catalog: catalog installed
6. Install metadata link: metadata linked

## Rules that hold
NoDeadlock
