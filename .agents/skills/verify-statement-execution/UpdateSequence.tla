---- MODULE UpdateSequence ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES phase, first, second, visible
vars == <<phase,first,second,visible>>
Init == /\ phase="select" /\ first=0 /\ second=0 /\ visible= <<0,0>>
Select == /\ phase="select" /\ phase'="first assignment" /\ UNCHANGED <<first,second,visible>>
AssignFirst == /\ phase="first assignment" /\ first'=1 /\ phase'="second assignment" /\ UNCHANGED <<second,visible>>
AssignSecond == /\ phase="second assignment" /\ second'=(IF Broken THEN 0 ELSE first) /\ phase'="validate" /\ UNCHANGED <<first,visible>>
Validate == /\ phase="validate" /\ phase'="publish" /\ UNCHANGED <<first,second,visible>>
Publish == /\ phase="publish" /\ visible'= <<first,second>> /\ phase'="complete" /\ UNCHANGED <<first,second>>
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Select \/ AssignFirst \/ AssignSecond \/ Validate \/ Publish \/ Done
AssignmentsSeePriorValues == phase="complete" => visible= <<1,1>>
====
