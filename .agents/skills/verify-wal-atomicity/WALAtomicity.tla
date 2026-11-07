---------------------------- MODULE WALAtomicity ----------------------------
EXTENDS Naturals, Sequences

Tables == {"items", "audit"}
EmptyRows == [table \in Tables |-> {}]
OldRows == [table \in Tables |-> {"a", "b"}]
NewRows == [table \in Tables |-> {"b"}]
Legacy == <<"items a", "items b", "audit a", "audit b">>
Replacement == <<"begin", "clear items", "items b", "clear audit", "audit b", "commit">>
Later == <<"begin", "items c", "audit c", "commit">>

ApplyOperation(rows, operation) ==
    CASE operation = "clear items" -> [rows EXCEPT !["items"] = {}]
      [] operation = "clear audit" -> [rows EXCEPT !["audit"] = {}]
      [] operation = "items a" -> [rows EXCEPT !["items"] = @ \cup {"a"}]
      [] operation = "items b" -> [rows EXCEPT !["items"] = @ \cup {"b"}]
      [] operation = "items c" -> [rows EXCEPT !["items"] = @ \cup {"c"}]
      [] operation = "audit a" -> [rows EXCEPT !["audit"] = @ \cup {"a"}]
      [] operation = "audit b" -> [rows EXCEPT !["audit"] = @ \cup {"b"}]
      [] operation = "audit c" -> [rows EXCEPT !["audit"] = @ \cup {"c"}]

RECURSIVE ApplyOperations(_, _)
ApplyOperations(rows, operations) ==
    IF Len(operations) = 0 THEN rows
    ELSE ApplyOperations(ApplyOperation(rows, Head(operations)), Tail(operations))

VARIABLES phase, log, tail, visible, expected, acknowledged,
          replayIndex, pending, attempt, writeIndex
vars == <<phase, log, tail, visible, expected, acknowledged,
          replayIndex, pending, attempt, writeIndex>>

WritingRecords == IF attempt = 1 THEN Replacement ELSE Later
CommittedRows == IF attempt = 1 THEN NewRows
                 ELSE [table \in Tables |-> expected[table] \cup {"c"}]

Init ==
    /\ phase = "writing"
    /\ log = Legacy
    /\ tail = "none"
    /\ visible = OldRows
    /\ expected = OldRows
    /\ acknowledged = FALSE
    /\ replayIndex = 1
    /\ pending = "none"
    /\ attempt = 1
    /\ writeIndex = 1

WriteLength ==
    /\ phase = "writing" /\ writeIndex <= Len(WritingRecords) /\ tail = "none"
    /\ tail' = "length"
    /\ UNCHANGED <<phase, log, visible, expected, acknowledged,
                    replayIndex, pending, attempt, writeIndex>>

WriteChecksum ==
    /\ phase = "writing" /\ tail = "length"
    /\ tail' = "checksum"
    /\ UNCHANGED <<phase, log, visible, expected, acknowledged,
                    replayIndex, pending, attempt, writeIndex>>

WritePayload ==
    /\ phase = "writing" /\ tail = "checksum"
    /\ log' = Append(log, WritingRecords[writeIndex])
    /\ expected' = IF WritingRecords[writeIndex] = "commit" THEN CommittedRows ELSE expected
    /\ writeIndex' = writeIndex + 1
    /\ tail' = "none"
    /\ UNCHANGED <<phase, visible, acknowledged, replayIndex, pending, attempt>>

SyncWAL ==
    /\ phase = "writing" /\ writeIndex > Len(WritingRecords) /\ tail = "none"
    /\ phase' = "synced"
    /\ UNCHANGED <<log, tail, visible, expected, acknowledged,
                    replayIndex, pending, attempt, writeIndex>>

PublishRows ==
    /\ phase = "synced"
    /\ phase' = "published"
    /\ visible' = expected
    /\ UNCHANGED <<log, tail, expected, acknowledged, replayIndex, pending, attempt, writeIndex>>

ReturnSuccess ==
    /\ phase = "published"
    /\ phase' = "committed"
    /\ acknowledged' = TRUE
    /\ UNCHANGED <<log, tail, visible, expected, replayIndex, pending, attempt, writeIndex>>

Crash ==
    /\ phase \in {"writing", "synced", "published", "committed"}
    /\ phase' = "replaying"
    /\ visible' = EmptyRows
    /\ replayIndex' = 1
    /\ pending' = "none"
    /\ UNCHANGED <<log, tail, expected, acknowledged, attempt, writeIndex>>

ReplayBegin ==
    /\ phase = "replaying" /\ replayIndex <= Len(log)
    /\ log[replayIndex] = "begin"
    /\ pending' = <<>>
    /\ replayIndex' = replayIndex + 1
    /\ UNCHANGED <<phase, log, tail, visible, expected, acknowledged, attempt, writeIndex>>

ReplayOperation ==
    /\ phase = "replaying" /\ replayIndex <= Len(log)
    /\ log[replayIndex] \notin {"begin", "commit"}
    /\ visible' = IF pending = "none" THEN ApplyOperation(visible, log[replayIndex]) ELSE visible
    /\ pending' = IF pending = "none" THEN pending ELSE Append(pending, log[replayIndex])
    /\ replayIndex' = replayIndex + 1
    /\ UNCHANGED <<phase, log, tail, expected, acknowledged, attempt, writeIndex>>

ReplayCommit ==
    /\ phase = "replaying" /\ replayIndex <= Len(log)
    /\ log[replayIndex] = "commit" /\ pending # "none"
    /\ visible' = ApplyOperations(visible, pending)
    /\ pending' = "none"
    /\ replayIndex' = replayIndex + 1
    /\ UNCHANGED <<phase, log, tail, expected, acknowledged, attempt, writeIndex>>

FinishRecovery ==
    /\ phase = "replaying" /\ replayIndex > Len(log) /\ tail = "none"
    /\ phase' = "recovered"
    /\ pending' = "none"
    /\ UNCHANGED <<log, tail, visible, expected, acknowledged, replayIndex, attempt, writeIndex>>

RejectTornRecord ==
    /\ phase = "replaying" /\ replayIndex > Len(log) /\ tail # "none"
    /\ phase' = "refused"
    /\ UNCHANGED <<log, tail, visible, expected, acknowledged, replayIndex, pending, attempt, writeIndex>>

AppendLaterTransaction ==
    /\ phase = "recovered" /\ attempt = 1
    /\ phase' = "writing"
    /\ attempt' = 2
    /\ writeIndex' = 1
    /\ acknowledged' = FALSE
    /\ UNCHANGED <<log, tail, visible, expected, replayIndex, pending>>

Done == phase \in {"recovered", "refused"} /\ UNCHANGED vars

Next == WriteLength \/ WriteChecksum \/ WritePayload
        \/ SyncWAL \/ PublishRows \/ ReturnSuccess \/ Crash
        \/ ReplayBegin \/ ReplayOperation \/ ReplayCommit
        \/ FinishRecovery \/ RejectTornRecord \/ AppendLaterTransaction \/ Done

AtomicRecovery == phase = "recovered" => visible = expected
AcknowledgedRecovery ==
    phase = "recovered" /\ acknowledged =>
        IF attempt = 1 THEN visible = NewRows
        ELSE \A table \in Tables : "c" \in visible[table]
=============================================================================
