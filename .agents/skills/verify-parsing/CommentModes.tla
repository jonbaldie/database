---- MODULE CommentModes ----
EXTENDS Naturals, Sequences
CONSTANT BrokenQuote
VARIABLES cursor, mode, output
vars == <<cursor,mode,output>>
Input == <<"a","quoted marker","/*","comment text","*/","b">>
Init == /\ cursor=1 /\ mode="sql" /\ output= <<>>
Quoted == /\ cursor<=6 /\ Input[cursor]="quoted marker" /\ mode="sql" /\ cursor'=cursor+1 /\ output'=(IF BrokenQuote THEN output ELSE Append(output,"quoted marker")) /\ UNCHANGED mode
OpenComment == /\ cursor<=6 /\ Input[cursor]="/*" /\ mode="sql" /\ cursor'=cursor+1 /\ mode'="block comment" /\ UNCHANGED output
CommentText == /\ cursor<=6 /\ mode="block comment" /\ Input[cursor]#"*/" /\ cursor'=cursor+1 /\ UNCHANGED <<mode,output>>
CloseComment == /\ cursor<=6 /\ mode="block comment" /\ Input[cursor]="*/" /\ cursor'=cursor+1 /\ mode'="sql" /\ UNCHANGED output
Text == /\ cursor<=6 /\ mode="sql" /\ Input[cursor] \in {"a","b"} /\ cursor'=cursor+1 /\ output'=Append(output,Input[cursor]) /\ UNCHANGED mode
Done == /\ cursor=7 /\ UNCHANGED vars
Next == Quoted \/ OpenComment \/ CommentText \/ CloseComment \/ Text \/ Done
QuotedCommentSurvives == cursor=7 => output= <<"a","quoted marker","b">>
====
