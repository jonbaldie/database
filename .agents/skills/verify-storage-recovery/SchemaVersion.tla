---- MODULE SchemaVersion ----
EXTENDS Naturals
CONSTANT BrokenVersion
VARIABLES phase, schema, base, published
vars == <<phase,schema,base,published>>
Init == /\ phase="open" /\ schema=0 /\ base=0 /\ published=FALSE
Begin == /\ phase="open" /\ phase'="staged" /\ base'=schema /\ UNCHANGED <<schema,published>>
ChangeSchema == /\ phase="staged" /\ schema=0 /\ schema'=1 /\ UNCHANGED <<phase,base,published>>
Commit == /\ phase="staged" /\ phase'="finished" /\ published'=(BrokenVersion \/ base=schema) /\ UNCHANGED <<schema,base>>
Done == /\ phase="finished" /\ UNCHANGED vars
Next == Begin \/ ChangeSchema \/ Commit \/ Done
StaleSchemaStaysPrivate == published => base=schema
====
