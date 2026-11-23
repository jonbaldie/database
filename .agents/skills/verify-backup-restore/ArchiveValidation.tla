----------------------- MODULE ArchiveValidation -----------------------
EXTENDS Naturals, FiniteSets
CONSTANT BrokenManifest
VARIABLES phase, manifestFound, seen
vars == <<phase, manifestFound, seen>>
Paths == {"instance.json", "catalog.json"}
Init == /\ phase = "reading" /\ manifestFound = FALSE /\ seen = {}
ReadFile == /\ phase = "reading" /\ \E path \in Paths:
             /\ phase' = IF path \in seen THEN "rejected" ELSE phase
             /\ seen' = seen \cup {path}
             /\ UNCHANGED manifestFound
ReadManifest == /\ phase = "reading"
                /\ phase' = IF manifestFound THEN "rejected" ELSE phase
                /\ manifestFound' = TRUE /\ UNCHANGED <<seen>>
UnsafeEntry == /\ phase = "reading" /\ phase' = "rejected" /\ UNCHANGED <<manifestFound, seen>>
Finish == /\ phase = "reading"
          /\ phase' = IF manifestFound \/ BrokenManifest THEN "entries read" ELSE "rejected"
          /\ UNCHANGED <<manifestFound, seen>>
Done == /\ phase \in {"rejected", "entries read"} /\ UNCHANGED vars
Next == ReadFile \/ ReadManifest \/ UnsafeEntry \/ Finish \/ Done
ResultRequiresManifest == phase = "entries read" => manifestFound
=============================================================================
