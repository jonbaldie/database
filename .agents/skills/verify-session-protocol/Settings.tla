---------------- MODULE Settings ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, value, requested
vars == <<phase, value, requested>>
Init == /\ phase = 0 /\ value \in 1..2 /\ requested \in 0..3
Assign == /\ phase = 0 /\ phase' = 1 /\ value' = (IF requested > 0 /\ (Broken \/ requested <= 2) THEN requested ELSE value) /\ UNCHANGED requested
Done == /\ phase = 1 /\ UNCHANGED vars
Next == Assign \/ Done
Ceiling == value \in 1..2
=============================================
