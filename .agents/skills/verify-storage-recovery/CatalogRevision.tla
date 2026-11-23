---- MODULE CatalogRevision ----
EXTENDS Naturals
CONSTANT BrokenClaim
VARIABLES processed, claimed, successes
vars == <<processed,claimed,successes>>
Init == /\ processed=0 /\ claimed=FALSE /\ successes=0
ApplyRequest == /\ processed<2 /\ processed'=processed+1 /\ successes'=(IF ~claimed \/ BrokenClaim THEN successes+1 ELSE successes) /\ claimed'=TRUE
Done == /\ processed=2 /\ UNCHANGED vars
Next == ApplyRequest \/ Done
OneRevisionOneReplacement == successes<=1
====
