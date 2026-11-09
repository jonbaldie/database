---------------- MODULE LiveExplanation ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, entry
vars == <<phase, entry>>
Init == /\ phase = 0 /\ entry = "none"
BeginOld == /\ phase = 0 /\ phase' = 1 /\ entry' = "old"
BeginNew == /\ phase = 1 /\ phase' = 2 /\ entry' = "new"
FinishOld == /\ phase = 2 /\ phase' = 3 /\ entry' = (IF Broken THEN "none" ELSE entry)
FinishNew == /\ phase = 3 /\ phase' = 4 /\ entry' = "none"
Done == /\ phase = 4 /\ UNCHANGED vars
Next == BeginOld \/ BeginNew \/ FinishOld \/ FinishNew \/ Done
CleanupOwnsEntry == phase = 3 => entry = "new"
=============================================
