------------------------- MODULE EngineLifecycle -------------------------
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, closed, finished, staged, outcome
vars == <<phase, closed, finished, staged, outcome>>
Init == /\ phase = "active" /\ closed = FALSE /\ finished = FALSE /\ staged = FALSE /\ outcome = "none"
Stage == /\ phase = "active" /\ phase' = "staged" /\ staged' = TRUE /\ UNCHANGED <<closed, finished, outcome>>
RejectValidation == /\ phase = "staged" /\ phase' = "failed"
                    /\ finished' = (Fault = "failure") /\ outcome' = "validation error" /\ UNCHANGED <<closed, staged>>
Commit == /\ phase = "staged" /\ ~closed /\ phase' = "committed"
          /\ finished' = (Fault # "success") /\ staged' = FALSE /\ outcome' = "success" /\ UNCHANGED closed
RepeatCommit == /\ phase = "committed" /\ phase' = "repeated"
                /\ outcome' = (IF Fault = "repeat" THEN "success" ELSE "finished error") /\ UNCHANGED <<closed, finished, staged>>
Close == /\ phase \in {"active", "staged", "failed"} /\ phase' = "closed" /\ closed' = TRUE /\ UNCHANGED <<finished, staged, outcome>>
CommitClosed == /\ phase = "closed" /\ phase' = "closed commit"
                /\ outcome' = (IF Fault = "closed commit" THEN "success" ELSE "closed error")
                /\ finished' = (Fault = "closed commit") /\ UNCHANGED <<closed, staged>>
BeginClosed == /\ phase = "closed commit" /\ phase' = "closed begin"
               /\ outcome' = (IF Fault = "closed begin" THEN "success" ELSE "closed error") /\ UNCHANGED <<closed, finished, staged>>
Done == /\ phase \in {"repeated", "closed begin"} /\ UNCHANGED vars
Next == Stage \/ RejectValidation \/ Commit \/ RepeatCommit \/ Close \/ CommitClosed \/ BeginClosed \/ Done
FailedCommitUnfinished == phase = "failed" => ~finished
SuccessFinishes == phase = "committed" => finished
RepeatRejected == phase = "repeated" => outcome = "finished error"
ClosedCommitRejected == phase = "closed commit" => outcome = "closed error" /\ ~finished
ClosedBeginRejected == phase = "closed begin" => outcome = "closed error"
=============================================================================
