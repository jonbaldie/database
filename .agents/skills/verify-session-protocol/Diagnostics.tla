---------------- MODULE Diagnostics ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, caller, stored
vars == <<phase, caller, stored>>
Init == /\ phase = 0 /\ caller = "original" /\ stored = "none"
Replace == /\ phase = 0 /\ phase' = 1 /\ stored' = caller /\ UNCHANGED caller
CallerChanges == /\ phase = 1 /\ phase' = 2 /\ caller' = "changed" /\ stored' = (IF Broken THEN "changed" ELSE stored)
Done == /\ phase = 2 /\ UNCHANGED vars
Next == Replace \/ CallerChanges \/ Done
OwnsDiagnostics == phase = 2 => stored = "original"
=============================================
