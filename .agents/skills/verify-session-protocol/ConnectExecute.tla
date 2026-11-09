---------------- MODULE ConnectExecute ----------------
EXTENDS Naturals
CONSTANT BrokenWatch, BrokenFinish
VARIABLES phase, running, watch, explanation
vars == <<phase,running,watch,explanation>>
Init == /\ phase = "Connect" /\ running = FALSE /\ watch = FALSE /\ explanation = FALSE
Greeting == /\ phase = "Connect" /\ phase' = "Greeting sent" /\ UNCHANGED <<running,watch,explanation>>
AuthenticationFails == /\ phase = "Greeting sent" /\ phase' = "Rejected" /\ UNCHANGED <<running,watch,explanation>>
AuthenticationSucceeds == /\ phase = "Greeting sent" /\ phase' = "Credentials accepted" /\ UNCHANGED <<running,watch,explanation>>
CapacityFull == /\ phase = "Credentials accepted" /\ phase' = "Rejected" /\ UNCHANGED <<running,watch,explanation>>
Admit == /\ phase = "Credentials accepted" /\ phase' = "Ready" /\ UNCHANGED <<running,watch,explanation>>
Start == /\ phase = "Ready" /\ phase' = "Statement starting" /\ explanation' = TRUE /\ UNCHANGED <<running,watch>>
InstallWatch == /\ phase = "Statement starting" /\ phase' = "Cancellation available" /\ watch' = ~BrokenWatch /\ UNCHANGED <<running,explanation>>
ShowActive == /\ phase = "Cancellation available" /\ phase' = "Statement active" /\ running' = TRUE /\ UNCHANGED <<watch,explanation>>
Cancel == /\ phase = "Statement active" /\ phase' = "Cancellation requested" /\ UNCHANGED <<running,watch,explanation>>
Finish == /\ phase \in {"Statement active","Cancellation requested"} /\ phase' = "Statement complete" /\ running' = BrokenFinish /\ watch' = FALSE /\ explanation' = FALSE
Disconnect == /\ phase = "Statement complete" /\ phase' = "Closed" /\ UNCHANGED <<running,watch,explanation>>
Done == /\ phase \in {"Closed","Rejected"} /\ UNCHANGED vars
Next == Greeting \/ AuthenticationFails \/ AuthenticationSucceeds \/ CapacityFull \/ Admit \/ Start \/ InstallWatch \/ ShowActive \/ Cancel \/ Finish \/ Disconnect \/ Done
RunningHasWatch == running => watch
CompletionHidesState == phase = "Statement complete" => ~running /\ ~explanation
=======================================================
