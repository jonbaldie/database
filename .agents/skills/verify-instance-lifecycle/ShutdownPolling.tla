---- MODULE ShutdownPolling ----
EXTENDS Naturals
CONSTANTS BrokenReset, BrokenThreshold
VARIABLES phase, streak, remembered, calls
vars == <<phase,streak,remembered,calls>>
Init == /\ phase="polling" /\ streak=0 /\ remembered=0 /\ calls=0
DialFails == /\ phase="polling" /\ calls<6 /\ streak'=streak+1 /\ remembered'=remembered+1 /\ calls'=calls+1 /\ phase'=(IF remembered+1 >= (IF BrokenThreshold THEN 1 ELSE 3) THEN "stopped" ELSE "polling")
DialSucceeds == /\ phase="polling" /\ calls<6 /\ streak'=0 /\ remembered'=(IF BrokenReset THEN remembered ELSE 0) /\ calls'=calls+1 /\ UNCHANGED phase
Deadline == /\ phase="polling" /\ calls=6 /\ phase'="timeout" /\ UNCHANGED <<streak,remembered,calls>>
Done == /\ phase \in {"stopped","timeout"} /\ UNCHANGED vars
Next == DialFails \/ DialSucceeds \/ Deadline \/ Done
ThreeConsecutiveFailures == phase="stopped" => streak>=3
====
