--------------------------- MODULE QuotedSQL ---------------------------
EXTENDS Naturals, Sequences
CONSTANTS BrokenEscape, BrokenDoubled
VARIABLES input, cursor, phase, accepted
vars == <<input,cursor,phase,accepted>>
Inputs == {<<"q","a","slash","q","q">>, <<"q","a","q","q","a","q">>, <<"q","a","q">>, <<"q","slash","q">>}
Current == IF cursor <= Len(input) THEN input[cursor] ELSE "end"
NextIsQuote == IF cursor < Len(input) THEN input[cursor+1] = "q" ELSE FALSE
Init == /\ input \in Inputs /\ cursor = 2 /\ phase = "scan" /\ accepted = FALSE
Escape == /\ phase = "scan" /\ cursor < Len(input) /\ Current = "slash" /\ ~BrokenEscape
          /\ cursor' = cursor+2 /\ UNCHANGED <<input,phase,accepted>>
Doubled == /\ phase = "scan" /\ cursor < Len(input) /\ Current = "q" /\ NextIsQuote /\ ~BrokenDoubled
           /\ cursor' = cursor+2 /\ UNCHANGED <<input,phase,accepted>>
Close == /\ phase = "scan" /\ cursor <= Len(input) /\ Current = "q"
         /\ (cursor = Len(input) \/ ~NextIsQuote \/ BrokenDoubled)
         /\ phase' = "done" /\ accepted' = TRUE /\ UNCHANGED <<input,cursor>>
Ordinary == /\ phase = "scan" /\ cursor <= Len(input) /\ Current # "q"
            /\ (Current # "slash" \/ cursor = Len(input) \/ BrokenEscape)
            /\ cursor' = cursor+1 /\ UNCHANGED <<input,phase,accepted>>
Reject == /\ phase = "scan" /\ cursor > Len(input) /\ phase' = "done" /\ UNCHANGED <<input,cursor,accepted>>
Done == /\ phase = "done" /\ UNCHANGED vars
Next == Escape \/ Doubled \/ Close \/ Ordinary \/ Reject \/ Done
ClosingBoundary == accepted => cursor = Len(input) /\ input # <<"q","slash","q">>
=============================================================================
