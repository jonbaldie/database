------------------------- MODULE SavepointLocks -------------------------
EXTENDS Integers
CONSTANT Broken
VARIABLES phase, mutations, saved, held
vars == <<phase, mutations, saved, held>>
Init == /\ phase = "active" /\ mutations = 0 /\ saved = -1 /\ held = 0
Save == /\ phase = "active" /\ saved = -1 /\ saved' = mutations /\ phase' = "saved" /\ UNCHANGED <<mutations, held>>
WriteAndLock == /\ phase = "saved" /\ mutations' = mutations + 1 /\ held' = 1 /\ phase' = "changed" /\ UNCHANGED saved
RollbackTo == /\ phase = "changed" /\ mutations' = saved /\ held' = (IF Broken THEN 0 ELSE held) /\ phase' = "restored" /\ UNCHANGED saved
Finish == /\ phase = "restored" /\ phase' = "finished" /\ held' = 0 /\ saved' = -1 /\ mutations' = 0
Done == /\ phase = "finished" /\ UNCHANGED vars
Next == Save \/ WriteAndLock \/ RollbackTo \/ Finish \/ Done
RollbackRetainsLocks == phase = "restored" => held = 1
=============================================================================
