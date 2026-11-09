---- MODULE AccountHistory ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, exists, locked, revoked, canAuthenticate
vars == <<phase, exists, locked, revoked, canAuthenticate>>
Init == /\ phase="absent" /\ exists=FALSE /\ locked=FALSE /\ revoked=FALSE /\ canAuthenticate=FALSE
CreateAccount == /\ (phase="absent") /\ (phase'="created" /\ exists'=TRUE /\ canAuthenticate'=TRUE) /\ UNCHANGED <<locked,revoked>>
ChangePassword == /\ (phase="created") /\ (phase'="changed") /\ UNCHANGED <<exists,locked,revoked,canAuthenticate>>
LockAccount == /\ (phase="changed") /\ (phase'="locked" /\ locked'=TRUE /\ revoked'=(Fault#"revoke") /\ canAuthenticate'=(Fault="auth")) /\ UNCHANGED <<exists>>
UnlockAccount == /\ (phase="locked") /\ (phase'="unlocked" /\ locked'=FALSE /\ canAuthenticate'=TRUE) /\ UNCHANGED <<exists,revoked>>
Reconnect == /\ (phase="unlocked") /\ (phase'="reconnected" /\ revoked'=FALSE) /\ UNCHANGED <<exists,locked,canAuthenticate>>
DropAccount == /\ (phase="reconnected") /\ (phase'="dropped" /\ exists'=FALSE /\ canAuthenticate'=FALSE /\ revoked'=(Fault#"revoke")) /\ UNCHANGED <<locked>>
Done == /\ phase \in {"dropped"} /\ UNCHANGED vars
Next == CreateAccount \/ ChangePassword \/ LockAccount \/ UnlockAccount \/ Reconnect \/ DropAccount \/ Done
DisabledAccountCannotAuthenticate == (~exists \/ locked) => ~canAuthenticate
DisableRevokesSession == phase \in {"locked","dropped"} => revoked
====
