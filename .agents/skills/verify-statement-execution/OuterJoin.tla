---------------- MODULE OuterJoin ----------------
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES left, matched, phase, output
vars == <<left,matched,phase,output>>
Init == /\ left = 1 /\ matched = FALSE /\ phase = "match right rows" /\ output = <<>>
Match == /\ phase = "match right rows" /\ matched' = (left = 1) /\ output' = (IF left = 1 THEN Append(output,left) ELSE output) /\ phase' = "finish left row" /\ UNCHANGED left
Finish == /\ phase = "finish left row" /\ output' = (IF ~matched /\ ~Broken THEN Append(output,left) ELSE output) /\ left' = left+1 /\ phase' = (IF left = 2 THEN "done" ELSE "match right rows") /\ matched' = FALSE
Done == /\ phase = "done" /\ UNCHANGED vars
Next == Match \/ Finish \/ Done
PreserveUnmatchedLeft == phase = "done" => output = <<1,2>>
================================================
