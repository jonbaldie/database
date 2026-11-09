---- MODULE AlterPublication ----
EXTENDS Naturals
CONSTANT Broken
VARIABLES phase, valid, staged, visible
vars == <<phase,valid,staged,visible>>
Init == /\ phase="original schema" /\ valid \in BOOLEAN /\ staged=0 /\ visible=0
Clone == /\ phase="original schema" /\ phase'="private schema" /\ UNCHANGED <<valid,staged,visible>>
ApplyActions == /\ phase="private schema" /\ phase'="actions applied" /\ staged'=1 /\ UNCHANGED <<valid,visible>>
Validate == /\ phase="actions applied" /\ phase'=(IF valid \/ Broken THEN "validated" ELSE "rejected") /\ UNCHANGED <<valid,staged,visible>>
Publish == /\ phase="validated" /\ phase'="published" /\ visible'=staged /\ UNCHANGED <<valid,staged>>
Done == /\ phase \in {"published","rejected"} /\ UNCHANGED vars
Next == Clone \/ ApplyActions \/ Validate \/ Publish \/ Done
ValidateBeforePublication == ~valid => visible=0
====
