---- MODULE AdmissionDrain ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, stopping, running, closed
vars == <<phase, stopping, running, closed>>
Init == /\ phase="pending" /\ stopping=FALSE /\ running=FALSE /\ closed=FALSE
Admit == /\ (phase="pending") /\ (phase'="admitted") /\ UNCHANGED <<stopping,running,closed>>
BeginStatement == /\ (phase="admitted") /\ (phase'="executing" /\ running'=TRUE) /\ UNCHANGED <<stopping,closed>>
RequestDrain == /\ (phase="executing") /\ (phase'="draining" /\ stopping'=TRUE) /\ UNCHANGED <<running,closed>>
CompleteStatement == /\ (phase="draining") /\ (phase'="finished" /\ running'=FALSE) /\ UNCHANGED <<stopping,closed>>
CloseSessions == /\ (phase="finished" \/ (phase="draining" /\ Fault="early")) /\ (phase'="closed" /\ closed'=TRUE) /\ UNCHANGED <<stopping,running>>
Done == /\ phase \in {"closed"} /\ UNCHANGED vars
Next == Admit \/ BeginStatement \/ RequestDrain \/ CompleteStatement \/ CloseSessions \/ Done
DrainWaitsForStatement == closed => stopping /\ ~running
====
