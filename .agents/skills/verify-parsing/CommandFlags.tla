--------------------------- MODULE CommandFlags ---------------------------
EXTENDS Naturals, Sequences, FiniteSets
CONSTANTS BrokenDuplicate, BrokenEmpty
VARIABLES input, cursor, seen, phase, duplicate, empty
vars == <<input,cursor,seen,phase,duplicate,empty>>
Entries == [name : {"a","b"}, value : {"x",""}]
Init == /\ input \in [1..2 -> Entries] /\ cursor = 1 /\ seen = {} /\ phase = "parse" /\ duplicate = FALSE /\ empty = FALSE
Add == /\ phase = "parse" /\ cursor <= 2
       /\ (input[cursor].name \notin seen \/ BrokenDuplicate)
       /\ (input[cursor].value # "" \/ BrokenEmpty)
       /\ duplicate' = (duplicate \/ input[cursor].name \in seen)
       /\ empty' = (empty \/ input[cursor].value = "")
       /\ seen' = seen \cup {input[cursor].name} /\ cursor' = cursor+1 /\ UNCHANGED <<input,phase>>
Reject == /\ phase = "parse" /\ cursor <= 2
          /\ ((input[cursor].name \in seen /\ ~BrokenDuplicate) \/ (input[cursor].value = "" /\ ~BrokenEmpty))
          /\ phase' = "rejected" /\ UNCHANGED <<input,cursor,seen,duplicate,empty>>
Accept == /\ phase = "parse" /\ cursor = 3 /\ phase' = "accepted" /\ UNCHANGED <<input,cursor,seen,duplicate,empty>>
Done == /\ phase \in {"accepted","rejected"} /\ UNCHANGED vars
Next == Add \/ Reject \/ Accept \/ Done
NoDuplicateSetting == phase = "accepted" => ~duplicate
NoEmptyValue == phase = "accepted" => ~empty
=============================================================================
