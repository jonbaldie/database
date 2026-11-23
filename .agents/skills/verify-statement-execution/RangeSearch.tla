---------------- MODULE RangeSearch ----------------
EXTENDS Naturals
CONSTANT Broken
VARIABLES low, high, boundary
vars == <<low,high,boundary>>
Init == /\ low = 0 /\ high = 3 /\ boundary \in 0..3
Search == /\ low < high /\ LET mid == (low+high) \div 2 IN /\ low' = (IF mid >= boundary + (IF Broken THEN 1 ELSE 0) THEN low ELSE mid+1) /\ high' = (IF mid >= boundary + (IF Broken THEN 1 ELSE 0) THEN mid ELSE high) /\ UNCHANGED boundary
Done == /\ low = high /\ UNCHANGED vars
Next == Search \/ Done
InclusiveLowerBound == low = high => low = boundary
================================================
