---------------- MODULE LimitDelivery ----------------
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES cursor, offset, count, output
vars == <<cursor,offset,count,output>>
Init == /\ cursor = 0 /\ offset \in 0..3 /\ count \in 0..3 /\ output = <<>>
Scan == /\ cursor < 3 /\ Len(output) < count /\ cursor' = cursor+1 /\ output' = (IF cursor < offset + (IF Broken THEN 1 ELSE 0) THEN output ELSE Append(output,cursor)) /\ UNCHANGED <<offset,count>>
Done == /\ (cursor = 3 \/ Len(output) = count) /\ UNCHANGED vars
Next == Scan \/ Done
Expected == SelectSeq(<<0,1,2>>, LAMBDA x: x >= offset /\ x < offset+count)
ExactSlice == (cursor = 3 \/ Len(output) = count) => output = Expected
================================================
