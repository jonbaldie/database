---- MODULE AllocationLifecycle ----
EXTENDS Naturals
CONSTANTS BrokenDisabled, BrokenScan, BrokenReset
VARIABLES phase, enabled, nextValue, rowMax, exhausted, steps
vars == <<phase,enabled,nextValue,rowMax,exhausted,steps>>
Init == /\ phase="uninitialized" /\ enabled \in BOOLEAN /\ nextValue=0 /\ rowMax \in 0..3 /\ exhausted=FALSE /\ steps=0
Initialize == /\ phase="uninitialized" /\ phase'=(IF enabled THEN "ready" ELSE "disabled") /\ nextValue'=(IF ~enabled THEN IF BrokenDisabled THEN 1 ELSE 0 ELSE IF BrokenScan THEN 1 ELSE IF rowMax=3 THEN 3 ELSE rowMax+1) /\ exhausted'=(enabled /\ rowMax=3 /\ ~BrokenScan) /\ UNCHANGED <<enabled,rowMax,steps>>
Generate == /\ phase="ready" /\ ~exhausted /\ steps<3 /\ rowMax'=nextValue /\ nextValue'=(IF nextValue=3 THEN 3 ELSE nextValue+1) /\ exhausted'=(nextValue=3) /\ steps'=steps+1 /\ UNCHANGED <<phase,enabled>>
Explicit == /\ phase="ready" /\ steps<3 /\ \E value \in 1..3: /\ rowMax'=(IF value>rowMax THEN value ELSE rowMax) /\ nextValue'=(IF exhausted \/ value<nextValue THEN nextValue ELSE IF value=3 THEN 3 ELSE value+1) /\ exhausted'=(exhausted \/ value=3) /\ steps'=steps+1 /\ UNCHANGED <<phase,enabled>>
Truncate == /\ phase="ready" /\ steps<3 /\ phase'="truncated" /\ rowMax'=0 /\ nextValue'=(IF BrokenReset THEN nextValue ELSE 1) /\ exhausted'=(IF BrokenReset THEN exhausted ELSE FALSE) /\ steps'=steps+1 /\ UNCHANGED enabled
Continue == /\ phase="truncated" /\ phase'="ready" /\ UNCHANGED <<enabled,nextValue,rowMax,exhausted,steps>>
Done == /\ phase \in {"disabled","ready"} /\ UNCHANGED vars
Next == Initialize \/ Generate \/ Explicit \/ Truncate \/ Continue \/ Done
AllocationLifecycleSafe == (phase="disabled" => nextValue=0 /\ ~exhausted) /\ (phase="ready" => exhausted \/ nextValue>rowMax) /\ (phase="truncated" => nextValue=1 /\ ~exhausted /\ rowMax=0)
====
