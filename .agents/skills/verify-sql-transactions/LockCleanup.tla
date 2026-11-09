---- MODULE LockCleanup ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, waiting, locks, saved
vars == <<phase,waiting,locks,saved>>
Init == /\ phase="held" /\ waiting=FALSE /\ locks=1 /\ saved=1
UpgradeWait == /\ phase="held" /\ phase'="waiting for upgrade" /\ waiting'=TRUE /\ UNCHANGED <<locks,saved>>
WaitExit(reason) == /\ phase="waiting for upgrade" /\ phase'=reason /\ waiting'=(Fault="wait") /\ UNCHANGED <<locks,saved>>
GrantUpgrade == /\ phase="waiting for upgrade" /\ phase'="upgraded" /\ waiting'=FALSE /\ UNCHANGED <<locks,saved>>
LockNextRow == /\ phase="upgraded" /\ phase'="extra row locked" /\ locks'=2 /\ UNCHANGED <<waiting,saved>>
ReadFails == /\ phase="extra row locked" /\ phase'="read failed" /\ locks'=(IF Fault="restore" THEN locks ELSE saved) /\ UNCHANGED <<waiting,saved>>
Release == /\ phase \in {"cancelled","timed out","read failed"} /\ phase'="released" /\ locks'=0 /\ waiting'=FALSE /\ UNCHANGED saved
Done == /\ phase="released" /\ UNCHANGED vars
Next == UpgradeWait \/ WaitExit("cancelled") \/ WaitExit("timed out") \/ GrantUpgrade \/ LockNextRow \/ ReadFails \/ Release \/ Done
WaitExitClearsEdge == phase \in {"cancelled","timed out"} => ~waiting
FailedReadRestoresLocks == phase="read failed" => locks=saved
====
