---------------- MODULE Cancellation ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, closed, closes
vars == <<phase, closed, closes>>
Init == /\ phase = 0 /\ closed = FALSE /\ closes = 0
Cancel == /\ phase = 0 /\ phase' = 1 /\ closed' = TRUE /\ closes' = 1
CancelAgain == /\ phase = 1 /\ phase' = 2 /\ closed' = TRUE /\ closes' = (IF Broken THEN 2 ELSE 1)
Done == /\ phase = 2 /\ UNCHANGED vars
Next == Cancel \/ CancelAgain \/ Done
CloseOnce == closes <= 1
=============================================
