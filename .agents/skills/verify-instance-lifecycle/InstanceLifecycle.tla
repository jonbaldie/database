------------------------ MODULE InstanceLifecycle ------------------------
EXTENDS Naturals
CONSTANT BreakMarker, BreakReady, BreakRuntime, BreakClean, BreakRecovery
VARIABLES phase, marker, locked, ready, runtimeStarted, recovered, priorRunning
vars == <<phase, marker, locked, ready, runtimeStarted, recovered, priorRunning>>
Init == /\ phase = "unclaimed" /\ marker \in {"stopped", "running"}
        /\ priorRunning = (marker = "running") /\ locked = FALSE
        /\ ready = FALSE /\ runtimeStarted = FALSE /\ recovered = FALSE
ClaimDirectory == /\ phase = "unclaimed" /\ phase' = "claimed" /\ locked' = TRUE
                  /\ UNCHANGED <<marker, ready, runtimeStarted, recovered, priorRunning>>
MarkRunning == /\ phase = "claimed" /\ phase' = "starting" /\ marker' = "running"
               /\ recovered' = (IF BreakRecovery THEN FALSE ELSE marker = "running")
               /\ UNCHANGED <<locked, ready, runtimeStarted, priorRunning>>
StartRuntime == /\ phase = "starting" /\ phase' = "started" /\ runtimeStarted' = ~BreakRuntime
                /\ UNCHANGED <<marker, locked, ready, recovered, priorRunning>>
ReportReady == /\ phase = "started" /\ phase' = "ready" /\ ready' = TRUE
               /\ UNCHANGED <<marker, locked, runtimeStarted, recovered, priorRunning>>
RequestStop == /\ phase = "ready" /\ phase' = "draining" /\ ready' = BreakReady
               /\ UNCHANGED <<marker, locked, runtimeStarted, recovered, priorRunning>>
DrainRuntime == /\ phase = "draining" /\ phase' = "drained" /\ ready' = FALSE
                /\ runtimeStarted' = FALSE
                /\ UNCHANGED <<marker, locked, recovered, priorRunning>>
MarkStopped == /\ phase = "drained" /\ phase' = "clean marker" /\ marker' = (IF BreakClean THEN "running" ELSE "stopped")
               /\ UNCHANGED <<locked, ready, runtimeStarted, recovered, priorRunning>>
ReleaseClean == /\ phase = "clean marker" /\ phase' = "clean stop" /\ locked' = FALSE
                /\ UNCHANGED <<marker, ready, runtimeStarted, recovered, priorRunning>>
FailOrCrash == /\ phase \in {"starting", "started", "ready", "draining", "drained"}
               /\ phase' = "unclean stop" /\ locked' = FALSE /\ ready' = FALSE
               /\ runtimeStarted' = FALSE
               /\ marker' = IF BreakMarker THEN "stopped" ELSE marker
               /\ UNCHANGED <<recovered, priorRunning>>
Done == /\ phase \in {"clean stop", "unclean stop"} /\ UNCHANGED vars
Next == ClaimDirectory \/ MarkRunning \/ StartRuntime \/ ReportReady \/ RequestStop
        \/ DrainRuntime \/ MarkStopped \/ ReleaseClean \/ FailOrCrash \/ Done
ReadinessRequiresRuntime == ready => locked /\ runtimeStarted
DrainingIsNotReady == phase = "draining" => ~ready
UncleanStopPreservesMarker == phase = "unclean stop" => marker = "running"
CleanStopHasStoppedMarker == phase = "clean stop" => marker = "stopped" /\ ~locked
RecoveryReflectsPriorMarker == phase \notin {"unclaimed", "claimed"} => recovered = priorRunning
=============================================================================
