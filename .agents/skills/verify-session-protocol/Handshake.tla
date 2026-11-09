---------------- MODULE Handshake ----------------
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, flags, accepted
vars == <<phase, flags, accepted>>
Init == /\ phase = 0 /\ flags \in SUBSET {"protocol", "secure", "plugin"} /\ accepted = FALSE
ValidateFlags == /\ phase = 0 /\ phase' = 1 /\ accepted' = (IF Broken THEN {"secure","plugin"} \subseteq flags ELSE flags = {"protocol","secure","plugin"}) /\ UNCHANGED flags
Done == /\ phase = 1 /\ UNCHANGED vars
Next == ValidateFlags \/ Done
RequiredFlags == accepted => flags = {"protocol","secure","plugin"}
=============================================
