---- MODULE ScalarSequence ----
EXTENDS Naturals
CONSTANT Broken
VARIABLES phase, available, read, outcome
vars == <<phase,available,read,outcome>>
Init == /\ phase="open subquery" /\ available \in 0..2 /\ read=0 /\ outcome="pending"
Read == /\ phase="open subquery" /\ read<available /\ read'=read+1 /\ phase'=(IF read=1 THEN "finish" ELSE phase) /\ UNCHANGED <<available,outcome>>
End == /\ phase="open subquery" /\ read=available /\ phase'="finish" /\ UNCHANGED <<available,read,outcome>>
Finish == /\ phase="finish" /\ phase'="released" /\ outcome'=(IF read=2 /\ ~Broken THEN "error" ELSE IF read=0 THEN "null" ELSE "value") /\ UNCHANGED <<available,read>>
Done == /\ phase="released" /\ UNCHANGED vars
Next == Read \/ End \/ Finish \/ Done
ScalarCardinality == phase="released" => outcome=(IF available=2 THEN "error" ELSE IF available=0 THEN "null" ELSE "value")
====
