---------------- MODULE Admission ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, pending, sessions
vars == <<phase, pending, sessions>>
Init == /\ phase = 0 /\ pending = 1 /\ sessions = 0
Admit == /\ phase = 0 /\ phase' = 1 /\ pending' = 0 /\ sessions' = 1
RegisterNext == /\ phase = 1 /\ phase' = 2 /\ pending' = 1 /\ UNCHANGED sessions
TryAdmitFull == /\ phase = 2 /\ phase' = 3 /\ pending' = (IF Broken THEN 0 ELSE pending) /\ sessions' = (IF Broken THEN 2 ELSE sessions)
Disconnect == /\ phase = 3 /\ phase' = 4 /\ pending' = 0 /\ sessions' = 0
Done == /\ phase = 4 /\ UNCHANGED vars
Next == Admit \/ RegisterNext \/ TryAdmitFull \/ Disconnect \/ Done
Capacity == sessions <= 1 /\ pending <= 1
=============================================
