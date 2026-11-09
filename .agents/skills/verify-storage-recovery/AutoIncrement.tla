---- MODULE AutoIncrement ----
EXTENDS Naturals, FiniteSets
CONSTANTS BrokenAdvance, BrokenExhaust
VARIABLES nextValue, exhausted, seen, steps
vars == <<nextValue,exhausted,seen,steps>>
Init == /\ nextValue=1 /\ exhausted=FALSE /\ seen={} /\ steps=0
Record(value) == /\ seen'=seen \cup {value} /\ steps'=steps+1 /\ nextValue'=(IF value<nextValue THEN nextValue ELSE IF value=3 THEN 3 ELSE IF BrokenAdvance THEN value ELSE value+1) /\ exhausted'=(exhausted \/ (value=3 /\ ~BrokenExhaust))
Allocate == /\ steps<3 /\ ~exhausted /\ Record(nextValue)
Explicit == /\ steps<3 /\ \E value \in 1..3: Record(value)
Done == /\ (steps=3 \/ exhausted) /\ UNCHANGED vars
Next == Allocate \/ Explicit \/ Done
FutureAboveRecordedValues == exhausted \/ (\A value \in seen: nextValue>value)
====
