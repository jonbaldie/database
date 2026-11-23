---- MODULE IndexBoundConstruction ----
EXTENDS Integers, FiniteSets
CONSTANT Broken
VARIABLES steps, lower, upper, lowerInclusive, upperInclusive, expected
vars == <<steps,lower,upper,lowerInclusive,upperInclusive,expected>>
Init == /\ steps=0 /\ lower=-1 /\ upper=3 /\ lowerInclusive=TRUE /\ upperInclusive=TRUE /\ expected=0..2
Lower(value,inclusive) == /\ steps<2 /\ steps'=steps+1 /\ lower'=(IF value>lower \/ (value=lower /\ ~inclusive /\ ~Broken) THEN value ELSE lower) /\ lowerInclusive'=(IF value>lower \/ (value=lower /\ ~inclusive /\ ~Broken) THEN inclusive ELSE lowerInclusive) /\ expected'={x \in expected: x>value \/ (x=value /\ inclusive)} /\ UNCHANGED <<upper,upperInclusive>>
Upper(value,inclusive) == /\ steps<2 /\ steps'=steps+1 /\ upper'=(IF value<upper \/ (value=upper /\ ~inclusive /\ ~Broken) THEN value ELSE upper) /\ upperInclusive'=(IF value<upper \/ (value=upper /\ ~inclusive /\ ~Broken) THEN inclusive ELSE upperInclusive) /\ expected'={x \in expected: x<value \/ (x=value /\ inclusive)} /\ UNCHANGED <<lower,lowerInclusive>>
Done == /\ steps=2 /\ UNCHANGED vars
Next == (\E value \in 0..2, inclusive \in BOOLEAN: Lower(value,inclusive) \/ Upper(value,inclusive)) \/ Done
BoundsMatchPredicates == {x \in 0..2: (x>lower \/ (x=lower /\ lowerInclusive)) /\ (x<upper \/ (x=upper /\ upperInclusive))}=expected
====
