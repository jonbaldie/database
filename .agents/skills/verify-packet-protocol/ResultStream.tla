---- MODULE ResultStream ----
EXTENDS Naturals
CONSTANT BrokenTail
VARIABLES phase, failed, terminal
vars == <<phase,failed,terminal>>
Init == /\ phase="validate" /\ failed=FALSE /\ terminal="none"
Valid == /\ phase="validate" /\ phase'="metadata" /\ UNCHANGED <<failed,terminal>>
Invalid == /\ phase="validate" /\ phase'="complete" /\ failed'=TRUE /\ terminal'="error"
Columns == /\ phase="metadata" /\ phase'="rows" /\ UNCHANGED <<failed,terminal>>
RowsDone == /\ phase="rows" /\ phase'="complete" /\ terminal'="EOF" /\ UNCHANGED failed
StreamFails == /\ phase="rows" /\ phase'="complete" /\ failed'=TRUE /\ terminal'=(IF BrokenTail THEN "EOF" ELSE "error")
TransportFails == /\ phase \in {"metadata","rows"} /\ phase'="complete" /\ failed'=TRUE /\ terminal'="transport aborted"
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Valid \/ Invalid \/ Columns \/ RowsDone \/ StreamFails \/ TransportFails \/ Done
ErrorDoesNotEndWithSuccess == failed => terminal # "EOF"
====
