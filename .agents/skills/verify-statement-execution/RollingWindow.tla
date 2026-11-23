---------------- MODULE RollingWindow ----------------
EXTENDS Integers
CONSTANT Broken
VARIABLES position, phase, activeCount
vars == <<position,phase,activeCount>>
Init == /\ position = 0 /\ phase = "ready" /\ activeCount = 1
Remove == /\ phase = "ready" /\ position < 2 /\ phase' = "old row removed" /\ activeCount' = activeCount - (IF Broken THEN 0 ELSE 1) /\ UNCHANGED position
Add == /\ phase = "old row removed" /\ phase' = "ready" /\ position' = position+1 /\ activeCount' = activeCount+1
Done == /\ phase = "ready" /\ position = 2 /\ UNCHANGED vars
Next == Remove \/ Add \/ Done
FrameCount == phase = "ready" => activeCount = 1
================================================
