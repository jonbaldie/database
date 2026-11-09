------------------------- MODULE OnlineCapture -------------------------
EXTENDS Naturals
CONSTANT BrokenRequired
VARIABLES phase, writers, count, cursor
vars == <<phase,writers,count,cursor>>
Files == {1,2}
Init == /\ phase = "reading chunks" /\ writers = [f \in Files |-> "absent"] /\ count = 0 /\ cursor = 1
ReadChunk == /\ phase = "reading chunks" /\ count < 3
             /\ \E f \in Files : writers' = [writers EXCEPT ![f] = "open"]
             /\ count' = count+1 /\ UNCHANGED <<phase,cursor>>
FinishRows == /\ phase = "reading chunks" /\ phase' = "finishing" /\ UNCHANGED <<writers,count,cursor>>
SyncFile == /\ phase = "finishing" /\ cursor <= 2 /\ writers[cursor] = "open"
            /\ writers' = [writers EXCEPT ![cursor] = "synced"] /\ UNCHANGED <<phase,count,cursor>>
CloseFile == /\ phase = "finishing" /\ cursor <= 2 /\ writers[cursor] = "synced"
             /\ writers' = [writers EXCEPT ![cursor] = "closed"] /\ UNCHANGED <<phase,count,cursor>>
Advance == /\ phase = "finishing" /\ cursor <= 2 /\ writers[cursor] = "closed"
           /\ cursor' = cursor+1 /\ UNCHANGED <<phase,writers,count>>
MissingFile == /\ phase = "finishing" /\ cursor <= 2 /\ writers[cursor] = "absent"
               /\ phase' = IF BrokenRequired THEN "complete" ELSE "failed"
               /\ UNCHANGED <<writers,count,cursor>>
Complete == /\ phase = "finishing" /\ cursor = 3 /\ phase' = "complete" /\ UNCHANGED <<writers,count,cursor>>
Done == /\ phase \in {"complete","failed"} /\ UNCHANGED vars
Next == ReadChunk \/ FinishRows \/ SyncFile \/ CloseFile \/ Advance \/ MissingFile \/ Complete \/ Done
CompleteHasBothClosedFiles == phase = "complete" => \A f \in Files : writers[f] = "closed"
=============================================================================
