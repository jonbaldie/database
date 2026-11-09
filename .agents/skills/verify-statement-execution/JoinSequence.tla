---- MODULE JoinSequence ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES phase, left, matched, output
vars == <<phase,left,matched,output>>
Init == /\ phase="build right" /\ left=1 /\ matched=FALSE /\ output= <<>>
Build == /\ phase="build right" /\ phase'="probe" /\ UNCHANGED <<left,matched,output>>
Probe == /\ phase="probe" /\ matched'=(left=1) /\ phase'="emit" /\ UNCHANGED <<left,output>>
Emit == /\ phase="emit" /\ output'=(IF matched THEN Append(output,<<left,1>>) ELSE IF Broken THEN output ELSE Append(output,<<left,0>>)) /\ phase'=(IF left=1 THEN "next left" ELSE "complete") /\ UNCHANGED <<left,matched>>
NextLeft == /\ phase="next left" /\ left'=2 /\ matched'=FALSE /\ phase'="probe" /\ UNCHANGED output
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Build \/ Probe \/ Emit \/ NextLeft \/ Done
MatchThenPreserveLeft == phase="complete" => output= << <<1,1>>,<<2,0>> >>
====
