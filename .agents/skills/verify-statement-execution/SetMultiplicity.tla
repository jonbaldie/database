---------------- MODULE SetMultiplicity ----------------
EXTENDS Naturals
CONSTANT Broken
VARIABLES left, right, remaining, cursor, emitted
vars == <<left,right,remaining,cursor,emitted>>
Init == /\ left \in 0..3 /\ right \in 0..3 /\ remaining = right /\ cursor = 0 /\ emitted = 0
Consume == /\ cursor < left /\ cursor' = cursor+1 /\ emitted' = emitted + (IF remaining > 0 THEN 1 ELSE 0) /\ remaining' = (IF remaining > 0 /\ ~Broken THEN remaining-1 ELSE remaining) /\ UNCHANGED <<left,right>>
Done == /\ cursor = left /\ UNCHANGED vars
Next == Consume \/ Done
IntersectionCount == cursor = left => emitted = (IF left < right THEN left ELSE right)
================================================
