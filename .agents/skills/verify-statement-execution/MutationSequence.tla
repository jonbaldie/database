---- MODULE MutationSequence ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES phase, candidate, staged, visible
vars == <<phase,candidate,staged,visible>>
Init == /\ phase="plan" /\ candidate=1 /\ staged=0 /\ visible=0
Plan == /\ phase="plan" /\ phase'="delete conflict" /\ UNCHANGED <<candidate,staged,visible>>
DeleteConflict == /\ phase="delete conflict" /\ staged'=0 /\ phase'="insert candidate" /\ UNCHANGED <<candidate,visible>>
InsertCandidate == /\ phase="insert candidate" /\ staged'=(IF Broken /\ candidate=2 THEN 1 ELSE candidate) /\ phase'="next" /\ UNCHANGED <<candidate,visible>>
NextCandidate == /\ phase="next" /\ candidate=1 /\ candidate'=2 /\ phase'="delete conflict" /\ UNCHANGED <<staged,visible>>
Validate == /\ phase="next" /\ candidate=2 /\ phase'="validated" /\ UNCHANGED <<candidate,staged,visible>>
Publish == /\ phase="validated" /\ visible'=staged /\ phase'="published" /\ UNCHANGED <<candidate,staged>>
Done == /\ phase="published" /\ UNCHANGED vars
Next == Plan \/ DeleteConflict \/ InsertCandidate \/ NextCandidate \/ Validate \/ Publish \/ Done
OrderedCandidates == phase="published" => visible=2
====
