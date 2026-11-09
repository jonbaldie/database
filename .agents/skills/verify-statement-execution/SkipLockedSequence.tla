---- MODULE SkipLockedSequence ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES position, offset, output, phase
vars == <<position,offset,output,phase>>
Init == /\ position=1 /\ offset=1 /\ output= <<>> /\ phase="skip offset"
Skip == /\ phase="skip offset" /\ position<=3 /\ position'=position+1 /\ offset'=(IF position#1 \/ Broken THEN offset-1 ELSE offset) /\ phase'=(IF (position#1 \/ Broken) /\ offset=1 THEN "acquire result" ELSE phase) /\ UNCHANGED output
Acquire == /\ phase="acquire result" /\ position<=3 /\ output'=Append(output,position) /\ phase'="complete" /\ UNCHANGED <<position,offset>>
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Skip \/ Acquire \/ Done
AvailableOffsetOnly == phase="complete" => output= <<3>>
====
