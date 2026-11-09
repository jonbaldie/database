---- MODULE SetFoldSequence ----
EXTENDS Naturals
CONSTANT Broken
VARIABLES phase, right, result
vars == <<phase,right,result>>
Init == /\ phase="terms evaluated" /\ right={2} /\ result={}
Intersect == /\ phase="terms evaluated" /\ right'=(IF Broken THEN {1,2} ELSE {2} \cap {2}) /\ phase'="fold union" /\ UNCHANGED result
Union == /\ phase="fold union" /\ result'=(IF Broken THEN right \cap {2} ELSE {1} \cup right) /\ phase'="complete" /\ UNCHANGED right
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Intersect \/ Union \/ Done
IntersectBeforeUnion == phase="complete" => result={1,2}
====
