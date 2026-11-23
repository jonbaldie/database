---- MODULE ListNesting ----
EXTENDS Naturals, Sequences
CONSTANT BrokenDepth
VARIABLES cursor, depth, parts
vars == <<cursor,depth,parts>>
Input == <<"(","a",",","b",")",",","c">>
Init == /\ cursor=1 /\ depth=0 /\ parts=1
Open == /\ cursor<=7 /\ Input[cursor]="(" /\ cursor'=cursor+1 /\ depth'=depth+1 /\ UNCHANGED parts
Close == /\ cursor<=7 /\ Input[cursor]=")" /\ cursor'=cursor+1 /\ depth'=depth-1 /\ UNCHANGED parts
Comma == /\ cursor<=7 /\ Input[cursor]="," /\ cursor'=cursor+1 /\ parts'=parts+(IF depth=0 \/ BrokenDepth THEN 1 ELSE 0) /\ UNCHANGED depth
Text == /\ cursor<=7 /\ Input[cursor] \in {"a","b","c"} /\ cursor'=cursor+1 /\ UNCHANGED <<depth,parts>>
Done == /\ cursor=8 /\ UNCHANGED vars
Next == Open \/ Close \/ Comma \/ Text \/ Done
OnlyOuterCommaSplits == cursor=8 => parts=2
====
