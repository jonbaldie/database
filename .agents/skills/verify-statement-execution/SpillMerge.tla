---------------- MODULE SpillMerge ----------------
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES left, right, output
vars == <<left,right,output>>
Init == /\ left = <<1,3>> /\ right = <<2>> /\ output = <<>>
PickLeft == IF Len(right) = 0 THEN TRUE ELSE IF Len(left) = 0 THEN FALSE ELSE (IF Broken THEN Head(left) > Head(right) ELSE Head(left) <= Head(right))
TakeLeft == /\ Len(left) > 0 /\ PickLeft /\ output' = Append(output,Head(left)) /\ left' = Tail(left) /\ UNCHANGED right
TakeRight == /\ Len(right) > 0 /\ ~PickLeft /\ output' = Append(output,Head(right)) /\ right' = Tail(right) /\ UNCHANGED left
Done == /\ Len(left) = 0 /\ Len(right) = 0 /\ UNCHANGED vars
Next == TakeLeft \/ TakeRight \/ Done
SortedRows == (Len(left) = 0 /\ Len(right) = 0) => output = <<1,2,3>>
================================================
