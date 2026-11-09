---- MODULE TableSchema ----
EXTENDS Naturals
CONSTANTS BrokenPersist, BrokenDrop
VARIABLES phase, operation, memory, durable, walOld, recoveredOld, success
vars == <<phase,operation,memory,durable,walOld,recoveredOld,success>>
Init == /\ phase="change" /\ operation=1 /\ memory=0 /\ durable=0 /\ walOld=FALSE /\ recoveredOld=FALSE /\ success=FALSE
ChangeShape == /\ phase="change" /\ phase'="memory changed" /\ memory'=(IF operation=1 \/ operation=4 THEN 1 ELSE IF operation=2 THEN 2 ELSE 0) /\ success'=FALSE /\ UNCHANGED <<operation,durable,walOld,recoveredOld>>
WriteTemporary == /\ phase="memory changed" /\ phase'="temporary written" /\ UNCHANGED <<operation,memory,durable,walOld,recoveredOld,success>>
SyncTemporary == /\ phase="temporary written" /\ phase'="temporary synced" /\ UNCHANGED <<operation,memory,durable,walOld,recoveredOld,success>>
RenameMetadata == /\ phase="temporary synced" /\ phase'="metadata renamed" /\ durable'=(IF BrokenPersist THEN durable ELSE memory) /\ UNCHANGED <<operation,memory,walOld,recoveredOld,success>>
SyncDirectory == /\ phase="metadata renamed" /\ phase'=(IF operation=3 THEN "drop checkpoint" ELSE "success") /\ success'=(operation#3) /\ UNCHANGED <<operation,memory,durable,walOld,recoveredOld>>
DropCheckpoint == /\ phase="drop checkpoint" /\ phase'="success" /\ walOld'=BrokenDrop /\ success'=TRUE /\ UNCHANGED <<operation,memory,durable,recoveredOld>>
NextOperation == /\ phase="success" /\ operation<4 /\ phase'="change" /\ operation'=operation+1 /\ walOld'=(IF operation=1 THEN TRUE ELSE walOld) /\ success'=FALSE /\ UNCHANGED <<memory,durable,recoveredOld>>
MetadataFailure == /\ phase \in {"memory changed","temporary written","temporary synced","metadata renamed"} /\ phase'="error" /\ success'=FALSE /\ UNCHANGED <<operation,memory,durable,walOld,recoveredOld>>
Restart == /\ phase="success" /\ operation=4 /\ phase'="recovered" /\ recoveredOld'=walOld /\ UNCHANGED <<operation,memory,durable,walOld,success>>
Done == /\ phase \in {"error","recovered"} /\ UNCHANGED vars
Next == ChangeShape \/ WriteTemporary \/ SyncTemporary \/ RenameMetadata \/ SyncDirectory \/ DropCheckpoint \/ NextOperation \/ MetadataFailure \/ Restart \/ Done
SchemaSuccessHasDurableImage == (success => memory=durable) /\ (phase="recovered" => ~recoveredOld)
====
