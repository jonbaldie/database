--------------------------- MODULE DepthGuard ---------------------------
EXTENDS Naturals
CONSTANTS BrokenBound, BrokenUnwind
VARIABLES initial, depth, phase
vars == <<initial,depth,phase>>
Init == /\ initial \in 0..3 /\ depth = initial /\ phase = "call"
Enter == /\ phase = "call" /\ depth' = depth+1 /\ phase' = "check" /\ UNCHANGED initial
Callback == /\ phase = "check" /\ (depth <= 2 \/ BrokenBound) /\ phase' = "callback" /\ UNCHANGED <<initial,depth>>
Reject == /\ phase = "check" /\ depth > 2 /\ ~BrokenBound /\ phase' = "rejected" /\ UNCHANGED <<initial,depth>>
Return == /\ phase \in {"callback","rejected"} /\ phase' = "returned" /\ depth' = (IF BrokenUnwind THEN depth ELSE depth-1) /\ UNCHANGED initial
Done == /\ phase = "returned" /\ UNCHANGED vars
Next == Enter \/ Callback \/ Reject \/ Return \/ Done
BoundedCallback == phase = "callback" => depth <= 2
DepthRestored == phase = "returned" => depth = initial
=============================================================================
