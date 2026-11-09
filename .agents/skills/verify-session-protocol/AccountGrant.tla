---------------- MODULE AccountGrant ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, grants
vars == <<phase, grants>>
Init == /\ phase = 0 /\ grants = {}
Grant == /\ phase = 0 /\ phase' = 1 /\ grants' = {"read"}
GrantAgain == /\ phase = 1 /\ phase' = 2 /\ grants' = grants \cup {"read"}
Revoke == /\ phase = 2 /\ phase' = 3 /\ grants' = (IF Broken THEN grants ELSE {})
Done == /\ phase = 3 /\ UNCHANGED vars
Next == Grant \/ GrantAgain \/ Revoke \/ Done
RevokeRemovesGrant == phase = 3 => grants = {}
=============================================
