---------------- MODULE ResultOwnership ----------------
EXTENDS Naturals
CONSTANT Broken
VARIABLES phase, owner, calls, attempts
vars == <<phase,owner,calls,attempts>>
Init == /\ phase = "result allocated" /\ owner = TRUE /\ calls = 0 /\ attempts = 0
Deliver == /\ phase = "result allocated" /\ phase' \in {"rows delivered","delivery failed"} /\ UNCHANGED <<owner,calls,attempts>>
Close == /\ attempts < 2 /\ phase' = "result closed" /\ attempts' = attempts+1 /\ calls' = calls + (IF owner THEN 1 ELSE 0) /\ owner' = IF Broken THEN owner ELSE FALSE
Done == /\ attempts = 2 /\ UNCHANGED vars
Next == Deliver \/ Close \/ Done
ReleaseOnce == calls <= 1
================================================
