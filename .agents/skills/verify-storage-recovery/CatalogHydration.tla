---- MODULE CatalogHydration ----
EXTENDS Naturals
CONSTANT BrokenHydration
VARIABLES phase, engineRows, catalogRows, visible
vars == <<phase,engineRows,catalogRows,visible>>
Init == /\ phase="open" /\ engineRows \in {"empty","durable"} /\ catalogRows="legacy" /\ visible="none"
Hydrate == /\ phase="open" /\ phase'="ready" /\ visible'=(IF engineRows="durable" /\ ~BrokenHydration THEN "durable" ELSE catalogRows) /\ UNCHANGED <<engineRows,catalogRows>>
Done == /\ phase="ready" /\ UNCHANGED vars
Next == Hydrate \/ Done
DurableRowsWin == phase="ready" /\ engineRows="durable" => visible="durable"
====
