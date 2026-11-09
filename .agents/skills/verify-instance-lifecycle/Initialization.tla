------------------------- MODULE Initialization -------------------------
EXTENDS Naturals
CONSTANT BrokenCleanup, Fault
VARIABLES phase, claim, catalog, metadata
vars == <<phase, claim, catalog, metadata>>
Init == /\ phase = "empty" /\ claim = FALSE /\ catalog = FALSE /\ metadata = FALSE
Claim == /\ phase = "empty" /\ phase' = "claimed" /\ claim' = TRUE
         /\ UNCHANGED <<catalog, metadata>>
StageCatalog == /\ phase = "claimed" /\ phase' = "catalog staged" /\ UNCHANGED <<claim, catalog, metadata>>
StageMetadata == /\ phase = "catalog staged" /\ phase' = "both staged" /\ UNCHANGED <<claim, catalog, metadata>>
LinkCatalog == /\ phase = "both staged" /\ phase' = "catalog linked" /\ catalog' = (Fault#"catalog") /\ UNCHANGED <<claim, metadata>>
UnlinkCatalogStage == /\ phase = "catalog linked" /\ phase' = "catalog installed" /\ UNCHANGED <<claim, catalog, metadata>>
LinkMetadata == /\ phase = "catalog installed" /\ phase' = "metadata linked" /\ metadata' = TRUE /\ UNCHANGED <<claim, catalog>>
UnlinkMetadataStage == /\ phase = "metadata linked" /\ phase' = "both installed" /\ UNCHANGED <<claim, catalog, metadata>>
Release == /\ phase = "both installed" /\ phase' = "initialized" /\ claim' = (Fault="release") /\ UNCHANGED <<catalog, metadata>>
MetadataConflict == /\ phase = "catalog installed" /\ phase' = "install failed" /\ UNCHANGED <<claim, catalog, metadata>>
RemoveOwnedCatalog == /\ phase = "install failed" /\ phase' = "cleaned" /\ catalog' = BrokenCleanup /\ UNCHANGED <<claim, metadata>>
DiscardClaim == /\ phase = "cleaned" /\ phase' = "failed" /\ claim' = FALSE /\ UNCHANGED <<catalog, metadata>>
Crash == /\ phase \notin {"empty", "initialized", "failed", "crashed"} /\ phase' = "crashed" /\ claim' = (Fault#"crash") /\ UNCHANGED <<catalog, metadata>>
Done == /\ phase \in {"initialized", "failed", "crashed"} /\ UNCHANGED vars
Next == Claim \/ StageCatalog \/ StageMetadata \/ LinkCatalog \/ UnlinkCatalogStage
        \/ LinkMetadata \/ UnlinkMetadataStage \/ Release \/ MetadataConflict
        \/ RemoveOwnedCatalog \/ DiscardClaim \/ Crash \/ Done
SuccessHasBothFiles == phase = "initialized" => catalog /\ metadata /\ ~claim
FailureRemovesOwnedCatalog == phase = "failed" => ~catalog
CrashRetainsClaim == phase = "crashed" => claim
MetadataNeedsCatalog == metadata => catalog
=============================================================================
