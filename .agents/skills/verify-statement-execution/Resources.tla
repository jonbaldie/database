---------------- MODULE Resources ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES held, closed, total
vars == <<held,closed,total>>
Init == /\ held = {} /\ closed = {} /\ total = 0
Reserve == \E i \in {1,2}: /\ i \notin held \cup closed /\ total < 2 /\ held' = held \cup {i} /\ total' = total+1 /\ UNCHANGED closed
Close == \E i \in {1,2}: /\ i \notin closed /\ closed' = closed \cup {i} /\ held' = held \ {i} /\ total' = total - (IF i \in held /\ ~Broken THEN 1 ELSE 0)
Done == /\ closed = {1,2} /\ UNCHANGED vars
Next == Reserve \/ Close \/ Done
ResourceAccounting == total = Cardinality(held)
================================================
