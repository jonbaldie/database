------------------------- MODULE JSONNesting -------------------------
EXTENDS Naturals, Sequences
CONSTANT Fault
VARIABLES input, pos, stack, phase
vars == <<input, pos, stack, phase>>
Inputs == {<<"v">>, <<"[", "[", "v", "]", "]">>, <<"{", "[", "v", "]", "}">>, <<"[", "[", "[", "v", "]", "]", "]">>, <<"[", "v">>, <<"[", "]">>, <<"v", "v">>}
Init == /\ input \in Inputs /\ pos = 1 /\ stack = <<>> /\ phase = "reading"
Open == /\ phase = "reading" /\ pos <= Len(input) /\ input[pos] \in {"[", "{"}
        /\ IF Len(stack) >= 2 /\ Fault # "depth"
              THEN /\ phase' = "rejected" /\ UNCHANGED <<pos, stack>>
              ELSE /\ stack' = Append(stack, IF input[pos] = "[" THEN "]" ELSE "}")
                   /\ pos' = pos + 1 /\ UNCHANGED phase
        /\ UNCHANGED input
Value == /\ phase = "reading" /\ pos <= Len(input) /\ input[pos] = "v"
         /\ pos' = pos + 1 /\ phase' = IF Len(stack) = 0 THEN "value complete" ELSE phase
         /\ UNCHANGED <<input, stack>>
Close == /\ phase = "reading" /\ pos <= Len(input) /\ input[pos] \in {"]", "}"}
         /\ IF Len(stack) > 0 /\ input[pos] = stack[Len(stack)]
               THEN /\ stack' = SubSeq(stack, 1, Len(stack)-1)
                    /\ pos' = pos+1 /\ phase' = IF Len(stack) = 1 THEN "value complete" ELSE phase
               ELSE /\ phase' = "rejected" /\ UNCHANGED <<pos, stack>>
         /\ UNCHANGED input
End == /\ phase = "reading" /\ pos > Len(input)
       /\ phase' = IF Fault = "end" THEN "accepted" ELSE "rejected"
       /\ UNCHANGED <<input, pos, stack>>
CheckEnd == /\ phase = "value complete"
            /\ phase' = IF pos > Len(input) \/ Fault = "trailing" THEN "accepted" ELSE "rejected"
            /\ UNCHANGED <<input, pos, stack>>
Done == /\ phase \in {"accepted", "rejected"} /\ UNCHANGED vars
Next == Open \/ Value \/ Close \/ End \/ CheckEnd \/ Done
DepthBound == Len(stack) <= 2
AcceptedComplete == phase = "accepted" => /\ Len(stack) = 0 /\ pos > Len(input)
=============================================================================
