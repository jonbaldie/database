---- MODULE MembershipSequence ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES values, position, unknown, matched, outcome
vars == <<values,position,unknown,matched,outcome>>
Init == /\ values \in {<<"null","other">>,<<"null","match">>,<<"other","other">>} /\ position=1 /\ unknown=FALSE /\ matched=FALSE /\ outcome="pending"
Read == /\ position<=2 /\ unknown'=(unknown \/ (values[position]="null" /\ ~Broken)) /\ matched'=(matched \/ values[position]="match") /\ position'=position+1 /\ UNCHANGED <<values,outcome>>
Finish == /\ position=3 /\ outcome="pending" /\ outcome'=(IF matched THEN "true" ELSE IF unknown THEN "null" ELSE "false") /\ UNCHANGED <<values,position,unknown,matched>>
Done == /\ outcome#"pending" /\ UNCHANGED vars
Next == Read \/ Finish \/ Done
NullAwareMembership == outcome#"pending" => outcome=(IF "match" \in {values[1],values[2]} THEN "true" ELSE IF "null" \in {values[1],values[2]} THEN "null" ELSE "false")
====
