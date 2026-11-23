---------------------------- MODULE Upgrade ----------------------------
EXTENDS Naturals
CONSTANT BrokenResume, Fault
VARIABLES phase, marker, version, metadataState, accepted
vars == <<phase, marker, version, metadataState, accepted>>
Init == /\ phase = "stopped" /\ marker = FALSE /\ version = "old"
        /\ metadataState = "stopped" /\ accepted = FALSE
WriteMarker == /\ phase = "stopped" /\ phase' = "marker written" /\ marker' = (Fault#"marker")
               /\ UNCHANGED <<version, metadataState, accepted>>
MarkIncomplete == /\ phase = "marker written" /\ phase' = "incomplete"
                  /\ metadataState' = "upgrade-incomplete" /\ UNCHANGED <<marker, version, accepted>>
WriteTarget == /\ phase = "incomplete" /\ phase' = "target written"
               /\ version' = (IF Fault="target" THEN "old" ELSE "target") /\ metadataState' = "stopped" /\ UNCHANGED <<marker, accepted>>
RemoveMarker == /\ phase = "target written" /\ phase' = "complete" /\ marker' = FALSE
                /\ UNCHANGED <<version, metadataState, accepted>>
Interrupt == /\ phase \in {"marker written", "incomplete", "target written"}
             /\ phase' = "interrupted" /\ UNCHANGED <<marker, version, metadataState, accepted>>
RequestDifferentTarget == /\ phase = "interrupted" /\ phase' = "different target requested"
                         /\ accepted' = BrokenResume /\ UNCHANGED <<marker, version, metadataState>>
RequestSameTarget == /\ phase = "interrupted" /\ phase' = "same target accepted"
                    /\ accepted' = TRUE /\ UNCHANGED <<marker, version, metadataState>>
Resume == /\ phase = "same target accepted" /\ phase' = "incomplete"
          /\ metadataState' = "upgrade-incomplete" /\ UNCHANGED <<marker, version, accepted>>
Done == /\ phase \in {"complete", "different target requested"} /\ UNCHANGED vars
Next == WriteMarker \/ MarkIncomplete \/ WriteTarget \/ RemoveMarker \/ Interrupt
        \/ RequestDifferentTarget \/ RequestSameTarget \/ Resume \/ Done
ResumeKeepsTarget == phase = "different target requested" => ~accepted
CompletedTargetInstalled == phase = "complete" => version = "target" /\ metadataState = "stopped" /\ ~marker
IncompleteHasMarker == metadataState = "upgrade-incomplete" => marker
=============================================================================
