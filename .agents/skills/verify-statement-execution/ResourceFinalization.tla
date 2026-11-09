---- MODULE ResourceFinalization ----
EXTENDS Naturals
CONSTANT Broken
VARIABLES phase, published, cancelled, timedOut, failure, memory
vars == <<phase,published,cancelled,timedOut,failure,memory>>
Init == /\ phase="admitted" /\ published=FALSE /\ cancelled=FALSE /\ timedOut=FALSE /\ failure=FALSE /\ memory=0
Reserve == /\ phase="admitted" /\ phase'="working" /\ memory'=1 /\ UNCHANGED <<published,cancelled,timedOut,failure>>
FailBeforeCommit == /\ phase="working" /\ phase'="failed" /\ failure'=TRUE /\ UNCHANGED <<published,cancelled,timedOut,memory>>
Publish == /\ phase="working" /\ phase'="finalized" /\ published'=TRUE /\ UNCHANGED <<cancelled,timedOut,failure,memory>>
Cancel == /\ phase="finalized" /\ phase'="late signal" /\ cancelled'=TRUE /\ UNCHANGED <<published,timedOut,failure,memory>>
Deadline == /\ phase="finalized" /\ phase'="late signal" /\ timedOut'=TRUE /\ UNCHANGED <<published,cancelled,failure,memory>>
Check == /\ phase="late signal" /\ phase'="checked" /\ failure'=Broken /\ UNCHANGED <<published,cancelled,timedOut,memory>>
Close == /\ phase \in {"checked","failed"} /\ phase'="closed" /\ memory'=0 /\ UNCHANGED <<published,cancelled,timedOut,failure>>
Done == /\ phase="closed" /\ UNCHANGED vars
Next == Reserve \/ FailBeforeCommit \/ Publish \/ Cancel \/ Deadline \/ Check \/ Close \/ Done
PublishedWorkStaysSuccessful == published => ~failure
====
