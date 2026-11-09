---- MODULE ShutdownLatch ----
EXTENDS Naturals
CONSTANT BrokenLatch
VARIABLES requests, operation, first, requested
vars == <<requests,operation,first,requested>>
Init == /\ requests=0 /\ operation="none" /\ first="none" /\ requested=FALSE
Request(id) == /\ requests<2 /\ requests'=requests+1
 /\ first'=(IF requests=0 THEN id ELSE first)
 /\ operation'=(IF ~requested \/ BrokenLatch THEN id ELSE operation)
 /\ requested'=TRUE
Done == /\ requests=2 /\ UNCHANGED vars
Next == (\E id \in {"a","b"}: Request(id)) \/ Done
FirstRequestStaysLatched == requested => operation=first
====
