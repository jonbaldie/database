# CommentModes

TLC reached 7 states and 7 steps. The chart draws 7 states and 6 arrows.
The chart does not draw 1 steps that stay in one chart state.

## QuotedCommentSurvives breaks
The shortest path has 6 steps:
0. Scan position 1 in sql
1. Text: Scan position 2 in sql
2. Quoted: Scan position 3 in sql
3. OpenComment: Scan position 4 in block comment
4. CommentText: Scan position 5 in block comment
5. CloseComment: Scan position 6 in sql
6. Text: Scan position 7 in sql

## Rules that hold
NoDeadlock
