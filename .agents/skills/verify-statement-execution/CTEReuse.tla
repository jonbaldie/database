---------------- MODULE CTEReuse ----------------
EXTENDS Naturals
CONSTANT Broken
VARIABLES cache, references, builds
vars == <<cache,references,builds>>
Init == /\ cache = FALSE /\ references = 0 /\ builds = 0
Materialize == /\ references < 2 /\ ~cache /\ builds' = builds+1 /\ references' = references+1 /\ cache' = ~Broken
Reuse == /\ references < 2 /\ cache /\ references' = references+1 /\ UNCHANGED <<cache,builds>>
Done == /\ references = 2 /\ UNCHANGED vars
Next == Materialize \/ Reuse \/ Done
BuildOnce == builds <= 1
================================================
