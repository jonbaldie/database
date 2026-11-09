---- MODULE WindowSequence ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES phase, position, rank, dense, results, frame
vars == <<phase,position,rank,dense,results,frame>>
Init == /\ phase="partition sorted" /\ position=1 /\ rank=1 /\ dense=1 /\ results= <<>> /\ frame=0
Advance == /\ phase="partition sorted" /\ frame'=position /\ rank'=(IF position=3 THEN (IF Broken THEN 2 ELSE 3) ELSE rank) /\ dense'=(IF position=3 THEN 2 ELSE dense) /\ phase'="frame ready" /\ UNCHANGED <<position,results>>
Emit == /\ phase="frame ready" /\ results'=Append(results,<<rank,dense,frame>>) /\ phase'=(IF position=3 THEN "complete" ELSE "partition sorted") /\ position'=position+1 /\ UNCHANGED <<rank,dense,frame>>
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Advance \/ Emit \/ Done
PeerRankAndFrame == phase="complete" => results= << <<1,1,1>>,<<1,1,2>>,<<3,2,3>> >>
====
