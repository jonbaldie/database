---------------- MODULE Prepared ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, bytes
vars == <<phase, bytes>>
Init == /\ phase = 0 /\ bytes = 0
AppendLongData == /\ phase = 0 /\ phase' = 1 /\ bytes' = 2
Reset == /\ phase = 1 /\ phase' = 2 /\ bytes' = (IF Broken THEN bytes ELSE 0)
Done == /\ phase = 2 /\ UNCHANGED vars
Next == AppendLongData \/ Reset \/ Done
ResetReleasesBytes == phase = 2 => bytes = 0
=============================================
