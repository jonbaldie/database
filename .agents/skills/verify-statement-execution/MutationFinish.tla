---------------- MODULE MutationFinish ----------------
EXTENDS Naturals
CONSTANT Broken
VARIABLES phase, transaction, staged
vars == <<phase,transaction,staged>>
Init == /\ phase = "mutation staged" /\ transaction = TRUE /\ staged = TRUE
Fail == /\ phase = "mutation staged" /\ phase' = "statement failed" /\ transaction' = Broken /\ staged' = Broken
Done == /\ phase = "statement failed" /\ UNCHANGED vars
Next == Fail \/ Done
RejectedAutocommitClearsStage == phase = "statement failed" => ~transaction /\ ~staged
================================================
