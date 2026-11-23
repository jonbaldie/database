---- MODULE SpillLifecycle ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES phase, files, output
vars == <<phase,files,output>>
Init == /\ phase="buffer rows" /\ files=0 /\ output= <<>>
Spill == /\ phase="buffer rows" /\ phase'="runs on disk" /\ files'=2 /\ UNCHANGED output
Merge == /\ phase="runs on disk" /\ phase'="merge ready" /\ UNCHANGED <<files,output>>
Deliver == /\ phase="merge ready" /\ phase'="delivered" /\ output'= <<1,2,3>> /\ UNCHANGED files
Close == /\ phase="delivered" /\ phase'="closed" /\ files'=(IF Broken THEN files ELSE 0) /\ UNCHANGED output
Done == /\ phase="closed" /\ UNCHANGED vars
Next == Spill \/ Merge \/ Deliver \/ Close \/ Done
DeliveredAndReleased == phase="closed" => output= <<1,2,3>> /\ files=0
====
