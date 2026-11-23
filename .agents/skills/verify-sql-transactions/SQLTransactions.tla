------------------------- MODULE SQLTransactions -------------------------
EXTENDS Integers
CONSTANT Broken, Fault
VARIABLES phase, dirty, mutations, saved, readSet, nextReadOnly, txReadOnly, outcome
vars == <<phase, dirty, mutations, saved, readSet, nextReadOnly, txReadOnly, outcome>>
Init == /\ phase = "idle" /\ dirty = FALSE /\ mutations = 0
        /\ saved = -1 /\ readSet = FALSE /\ nextReadOnly \in BOOLEAN
        /\ txReadOnly = FALSE /\ outcome = "none"
Begin == /\ phase = "idle" /\ phase' = "active"
         /\ txReadOnly' = nextReadOnly /\ nextReadOnly' = (IF Fault="one-shot" THEN nextReadOnly ELSE FALSE)
         /\ UNCHANGED <<dirty, mutations, saved, readSet, outcome>>
Read == /\ phase = "active" /\ ~readSet /\ readSet' = TRUE
        /\ UNCHANGED <<phase, dirty, mutations, saved, nextReadOnly, txReadOnly, outcome>>
Write == /\ phase = "active" /\ (~txReadOnly \/ Fault="read-only") /\ mutations < 2
         /\ mutations' = mutations + 1 /\ dirty' = TRUE /\ outcome' = "none"
         /\ UNCHANGED <<phase, saved, readSet, nextReadOnly, txReadOnly>>
Savepoint == /\ phase = "active" /\ saved = -1
             /\ saved' = mutations
             /\ UNCHANGED <<phase, dirty, mutations, readSet, nextReadOnly, txReadOnly, outcome>>
RollbackTo == /\ phase = "active" /\ saved >= 0
              /\ mutations' = IF Broken THEN mutations ELSE saved
              /\ dirty' = (saved > 0) /\ outcome' = "rolled back to savepoint"
              /\ UNCHANGED <<phase, saved, readSet, nextReadOnly, txReadOnly>>
Release == /\ phase = "active" /\ saved >= 0 /\ saved' = -1 /\ outcome' = "none"
           /\ UNCHANGED <<phase, dirty, mutations, readSet, nextReadOnly, txReadOnly>>
Finish(result) == /\ phase = "active" /\ phase' = "finished"
                  /\ dirty' = FALSE /\ mutations' = (IF Fault="cleanup" THEN 1 ELSE 0) /\ saved' = -1
                  /\ readSet' = FALSE /\ txReadOnly' = FALSE /\ outcome' = result
                  /\ UNCHANGED nextReadOnly
Commit == Finish("committed")
Rollback == Finish("rolled back")
Done == /\ phase = "finished" /\ UNCHANGED vars
Next == Begin \/ Read \/ Write \/ Savepoint \/ RollbackTo \/ Release \/ Commit \/ Rollback \/ Done
SavepointRestoresPrefix == outcome = "rolled back to savepoint" => mutations = saved
FinishedClearsState == phase = "finished" => /\ mutations = 0 /\ saved = -1 /\ ~dirty /\ ~readSet /\ ~txReadOnly
ReadOnlyHasNoMutations == txReadOnly => mutations = 0
OneShotConsumed == phase # "idle" => ~nextReadOnly
=============================================================================
