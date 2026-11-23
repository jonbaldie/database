----------------------- MODULE ArchivePublication -----------------------
EXTENDS Naturals
CONSTANT BrokenSync, BrokenFailure
VARIABLES phase, synced, closed, visible
vars == <<phase, synced, closed, visible>>
Init == /\ phase = "temporary open" /\ synced = FALSE /\ closed = FALSE /\ visible = "old"
Write == /\ phase = "temporary open" /\ phase' = "written" /\ UNCHANGED <<synced,closed,visible>>
Sync == /\ phase = "written" /\ phase' = "sync returned" /\ synced' = ~BrokenSync /\ UNCHANGED <<closed,visible>>
Close == /\ phase = "sync returned" /\ phase' = "closed" /\ closed' = TRUE /\ UNCHANGED <<synced,visible>>
Rename == /\ phase = "closed" /\ phase' = "published" /\ visible' = "new" /\ UNCHANGED <<synced,closed>>
Fail == /\ phase \in {"temporary open","written","sync returned","closed"}
        /\ phase' = "failed" /\ visible'=(IF BrokenFailure THEN "partial" ELSE visible) /\ UNCHANGED <<synced,closed>>
Done == /\ phase \in {"published","failed"} /\ UNCHANGED vars
Next == Write \/ Sync \/ Close \/ Rename \/ Fail \/ Done
PublicationRequiresSync == visible = "new" => synced /\ closed
FailurePreservesOldOutput == phase = "failed" => visible = "old"
=============================================================================
