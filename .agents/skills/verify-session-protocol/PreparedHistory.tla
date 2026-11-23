---- MODULE PreparedHistory ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, cachedTypes, longData, owned
vars == <<phase, cachedTypes, longData, owned>>
Init == /\ phase="absent" /\ cachedTypes=FALSE /\ longData=FALSE /\ owned=FALSE
Allocate == /\ (phase="absent") /\ (phase'="allocated" /\ owned'=TRUE) /\ UNCHANGED <<cachedTypes,longData>>
ExecuteWithTypes == /\ (phase="allocated") /\ (phase'="first execution" /\ cachedTypes'=(Fault#"types")) /\ UNCHANGED <<longData,owned>>
ReuseTypes == /\ (phase="first execution") /\ (phase'="second execution") /\ UNCHANGED <<cachedTypes,longData,owned>>
AppendLongData == /\ (phase="second execution") /\ (phase'="long data" /\ longData'=TRUE) /\ UNCHANGED <<cachedTypes,owned>>
ResetStatement == /\ (phase="long data") /\ (phase'="reset" /\ longData'=FALSE) /\ UNCHANGED <<cachedTypes,owned>>
ResetConnection == /\ (phase="reset") /\ (phase'="closed" /\ owned'=(Fault="close")) /\ UNCHANGED <<cachedTypes,longData>>
Done == /\ phase \in {"closed"} /\ UNCHANGED vars
Next == Allocate \/ ExecuteWithTypes \/ ReuseTypes \/ AppendLongData \/ ResetStatement \/ ResetConnection \/ Done
CachedTypesAvailable == phase="second execution" => cachedTypes
ConnectionResetReleasesPrepared == phase="closed" => ~owned /\ ~longData
====
