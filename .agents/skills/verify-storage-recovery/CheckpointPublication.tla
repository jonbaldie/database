---- MODULE CheckpointPublication ----
EXTENDS Naturals
CONSTANTS BrokenSnapshot, BrokenFence
VARIABLES phase, snapshot, oldLog, closed, rows
vars == <<phase,snapshot,oldLog,closed,rows>>
Init == /\ phase="start" /\ snapshot=FALSE /\ oldLog=TRUE /\ closed=FALSE /\ rows="new"
WriteSnapshot == /\ phase="start" /\ phase'="temporary snapshot synced" /\ UNCHANGED <<snapshot,oldLog,closed,rows>>
PublishSnapshot == /\ phase="temporary snapshot synced" /\ phase'="snapshot published" /\ snapshot'=~BrokenSnapshot /\ UNCHANGED <<oldLog,closed,rows>>
PrepareLog == /\ phase="snapshot published" /\ phase'="new log synced" /\ UNCHANGED <<snapshot,oldLog,closed,rows>>
ReplaceLog == /\ phase="new log synced" /\ phase'="log replaced" /\ oldLog'=FALSE /\ UNCHANGED <<snapshot,closed,rows>>
Finish == /\ phase="log replaced" /\ phase'="complete" /\ UNCHANGED <<snapshot,oldLog,closed,rows>>
DirectorySyncFails == /\ phase="log replaced" /\ phase'="directory sync failed" /\ closed'=~BrokenFence /\ UNCHANGED <<snapshot,oldLog,rows>>
Stop == /\ phase \notin {"restart","recovered"} /\ phase'="restart" /\ UNCHANGED <<snapshot,oldLog,closed,rows>>
Recover == /\ phase="restart" /\ phase'="recovered" /\ rows'=(IF snapshot \/ oldLog THEN "new" ELSE "lost") /\ UNCHANGED <<snapshot,oldLog,closed>>
Done == /\ phase="recovered" /\ UNCHANGED vars
Next == WriteSnapshot \/ PublishSnapshot \/ PrepareLog \/ ReplaceLog \/ Finish \/ DirectorySyncFails \/ Stop \/ Recover \/ Done
CheckpointKeepsRows == phase="recovered" => rows="new"
FailedRotationStopsWriter == phase="directory sync failed" => closed
====
