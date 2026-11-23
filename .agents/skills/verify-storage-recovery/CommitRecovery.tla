-------------------------- MODULE CommitRecovery --------------------------
EXTENDS Naturals
CONSTANTS BrokenCommit, BrokenReplay
VARIABLES phase, commitRecord, rows, acknowledged, operations
vars == <<phase, commitRecord, rows, acknowledged, operations>>
Init == /\ phase = "private changes" /\ commitRecord = FALSE /\ rows = "old" /\ acknowledged = FALSE /\ operations = FALSE
WriteBegin == /\ phase = "private changes" /\ phase' = "begin record" /\ UNCHANGED <<commitRecord,rows,acknowledged,operations>>
WriteOperations == /\ phase = "begin record" /\ phase' = "operations recorded" /\ operations' = TRUE /\ UNCHANGED <<commitRecord,rows,acknowledged>>
WriteCommit == /\ phase = "operations recorded" /\ phase' = "commit recorded" /\ commitRecord' = ~BrokenCommit /\ UNCHANGED <<rows,acknowledged,operations>>
Sync == /\ phase = "commit recorded" /\ phase' = "log synced" /\ UNCHANGED <<commitRecord,rows,acknowledged,operations>>
Publish == /\ phase = "log synced" /\ phase' = "rows published" /\ rows' = "new" /\ UNCHANGED <<commitRecord,acknowledged,operations>>
ReturnSuccess == /\ phase = "rows published" /\ phase' = "success returned" /\ acknowledged' = TRUE /\ UNCHANGED <<commitRecord,rows,operations>>
StopProcess == /\ phase \notin {"restarting","recovered"} /\ phase' = "restarting" /\ rows' = "unavailable" /\ UNCHANGED <<commitRecord,acknowledged,operations>>
Recover == /\ phase = "restarting" /\ phase' = "recovered" /\ rows' = (IF commitRecord \/ (BrokenReplay /\ operations) THEN "new" ELSE "old") /\ UNCHANGED <<commitRecord,acknowledged,operations>>
Done == /\ phase = "recovered" /\ UNCHANGED vars
Next == WriteBegin \/ WriteOperations \/ WriteCommit \/ Sync \/ Publish \/ ReturnSuccess \/ StopProcess \/ Recover \/ Done
AcknowledgedRowsSurviveRestart == phase = "recovered" /\ acknowledged => rows = "new"
UncommittedRowsStayHidden == phase = "recovered" /\ ~commitRecord => rows = "old"
=============================================================================
