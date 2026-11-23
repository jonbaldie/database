---- MODULE CommandHistory ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, pending, transaction, dispatched
vars == <<phase, pending, transaction, dispatched>>
Init == /\ phase="executing" /\ pending=FALSE /\ transaction=TRUE /\ dispatched=FALSE
ReadAhead == /\ (phase="executing") /\ (phase'="buffered" /\ pending'=(Fault#"read")) /\ UNCHANGED <<transaction,dispatched>>
StatementReturns == /\ (phase="buffered") /\ (phase'="idle") /\ UNCHANGED <<pending,transaction,dispatched>>
DispatchPending == /\ (phase="idle") /\ (phase'="dispatched" /\ dispatched'=pending) /\ UNCHANGED <<pending,transaction>>
ExpireTransaction == /\ (phase="idle") /\ (phase'="timeout" /\ transaction'=(Fault="timeout")) /\ UNCHANGED <<pending,dispatched>>
Revoke == /\ (phase="idle") /\ (phase'="revoked") /\ UNCHANGED <<pending,transaction,dispatched>>
CloseRevoked == /\ (phase="revoked") /\ (phase'="closed" /\ dispatched'=(Fault="revoke")) /\ UNCHANGED <<pending,transaction>>
Done == /\ phase \in {"dispatched","timeout","closed"} /\ UNCHANGED vars
Next == ReadAhead \/ StatementReturns \/ DispatchPending \/ ExpireTransaction \/ Revoke \/ CloseRevoked \/ Done
ReadAheadPreservesCommand == phase="dispatched" => dispatched
IdleExpiryRollsBack == phase="timeout" => ~transaction
RevocationStopsDispatch == phase="closed" => ~dispatched
====
