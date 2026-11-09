---- MODULE CatalogPublication ----
EXTENDS Naturals
CONSTANTS BrokenRows, BrokenSchema, BrokenNotify, BrokenFailure
VARIABLES phase, rowsSynced, schemaPersisted, published, outcome, failed
vars == <<phase,rowsSynced,schemaPersisted,published,outcome,failed>>
Init == /\ phase="queued" /\ rowsSynced=FALSE /\ schemaPersisted=FALSE /\ published=FALSE /\ outcome="waiting" /\ failed=FALSE
CollectBatch == /\ phase="queued" /\ phase'="batch collected" /\ UNCHANGED <<rowsSynced,schemaPersisted,published,outcome,failed>>
ApplyRequests == /\ phase="batch collected" /\ phase'="isolated changes applied" /\ outcome'=(IF BrokenNotify THEN "success" ELSE "waiting") /\ UNCHANGED <<rowsSynced,schemaPersisted,published,failed>>
RejectAllRequests == /\ phase="batch collected" /\ phase'= "complete" /\ outcome'="error" /\ failed'=TRUE /\ UNCHANGED <<rowsSynced,schemaPersisted,published>>
Validate == /\ phase="isolated changes applied" /\ phase'="validated" /\ UNCHANGED <<rowsSynced,schemaPersisted,published,outcome,failed>>
PrepareRows == /\ phase="validated" /\ phase'="row transaction staged" /\ UNCHANGED <<rowsSynced,schemaPersisted,published,outcome,failed>>
CommitRows == /\ phase="row transaction staged" /\ phase'="rows synced" /\ rowsSynced'=~BrokenRows /\ UNCHANGED <<schemaPersisted,published,outcome,failed>>
CheckpointSchema == /\ phase="rows synced" /\ phase'="row schema checkpointed" /\ UNCHANGED <<rowsSynced,schemaPersisted,published,outcome,failed>>
PersistSchema == /\ phase="row schema checkpointed" /\ phase'="schema persisted" /\ schemaPersisted'=~BrokenSchema /\ UNCHANGED <<rowsSynced,published,outcome,failed>>
PublishRevision == /\ phase="schema persisted" /\ phase'="revision published" /\ published'=TRUE /\ UNCHANGED <<rowsSynced,schemaPersisted,outcome,failed>>
NotifySuccess == /\ phase="revision published" /\ phase'="complete" /\ outcome'="success" /\ UNCHANGED <<rowsSynced,schemaPersisted,published,failed>>
Fail == /\ phase \in {"isolated changes applied","row transaction staged","row schema checkpointed"} /\ phase'="failed" /\ failed'=TRUE /\ UNCHANGED <<rowsSynced,schemaPersisted,published,outcome>>
NotifyFailure == /\ phase="failed" /\ phase'="complete" /\ outcome'=(IF BrokenFailure THEN "success" ELSE "error") /\ UNCHANGED <<rowsSynced,schemaPersisted,published,failed>>
Done == /\ phase="complete" /\ UNCHANGED vars
Next == CollectBatch \/ RejectAllRequests \/ ApplyRequests \/ Validate \/ PrepareRows \/ CommitRows \/ CheckpointSchema \/ PersistSchema \/ PublishRevision \/ NotifySuccess \/ Fail \/ NotifyFailure \/ Done
OutcomeMatchesPublication == (outcome="success" => published /\ rowsSynced /\ schemaPersisted /\ ~failed) /\ (failed => ~published)
====
