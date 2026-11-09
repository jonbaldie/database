---- MODULE SnapshotLifecycle ----
EXTENDS Integers
CONSTANT Fault
VARIABLES phase, revision, snapshot, observed, readSet, dirty, mode, published
vars == <<phase,revision,snapshot,observed,readSet,dirty,mode,published>>
Init == /\ phase="idle" /\ revision=0 /\ snapshot=-1 /\ observed=-1 /\ readSet=FALSE /\ dirty=FALSE /\ mode \in {"repeatable","committed"} /\ published=FALSE
Begin == /\ phase="idle" /\ phase'="active" /\ snapshot'=(IF Fault="eager" THEN revision ELSE -1) /\ UNCHANGED <<revision,observed,readSet,dirty,mode,published>>
ExternalPublish == /\ phase="active" /\ revision=0 /\ revision'=1 /\ UNCHANGED <<phase,snapshot,observed,readSet,dirty,mode,published>>
FirstRead == /\ phase="active" /\ ~readSet /\ readSet'=TRUE /\ snapshot'=revision /\ observed'=revision /\ phase'="read" /\ UNCHANGED <<revision,dirty,mode,published>>
LaterPublish == /\ phase="read" /\ revision'=revision+1 /\ phase'="updated outside" /\ UNCHANGED <<snapshot,observed,readSet,dirty,mode,published>>
ReadAgain == /\ phase="updated outside" /\ phase'="read again" /\ observed'=(IF mode="repeatable" /\ Fault#"refresh" THEN snapshot ELSE revision) /\ UNCHANGED <<revision,snapshot,readSet,dirty,mode,published>>
StageWrite == /\ phase="read again" /\ phase'="staged" /\ dirty'=TRUE /\ UNCHANGED <<revision,snapshot,observed,readSet,mode,published>>
Commit == /\ phase="staged" /\ phase'="committed" /\ published'=TRUE /\ dirty'=FALSE /\ UNCHANGED <<revision,snapshot,observed,readSet,mode>>
RejectCommit == /\ phase="staged" /\ phase'="rejected" /\ dirty'=(Fault="cleanup") /\ UNCHANGED <<revision,snapshot,observed,readSet,mode,published>>
RejectResources == /\ phase="staged" /\ phase'= "resource rejected" /\ dirty'=(Fault#"resources") /\ UNCHANGED <<revision,snapshot,observed,readSet,mode,published>>
Retry == /\ phase="resource rejected" /\ phase'="staged" /\ UNCHANGED <<revision,snapshot,observed,readSet,dirty,mode,published>>
Done == /\ phase \in {"committed","rejected"} /\ UNCHANGED vars
Next == Begin \/ ExternalPublish \/ FirstRead \/ LaterPublish \/ ReadAgain \/ StageWrite \/ Commit \/ RejectCommit \/ RejectResources \/ Retry \/ Done
BeginIsLazy == phase="active" => snapshot=-1
RepeatableReadKeepsImage == phase="read again" /\ mode="repeatable" => observed=snapshot
RejectedPublicationClearsStage == phase="rejected" => ~dirty /\ ~published
ResourceFailureKeepsStage == phase="resource rejected" => dirty /\ ~published
====
