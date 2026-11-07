---------------------------- MODULE WALAtomicity ----------------------------
EXTENDS Naturals, Sequences

\* The old rows are the durable state before the transaction. The staged
\* transaction replaces {a, b} with {b}: writeOverlay writes clear, then b.
OldRows == {"a", "b"}
NewRows == {"b"}

VARIABLES phase, log, tail, visible, acknowledged, replayIndex
vars == <<phase, log, tail, visible, acknowledged, replayIndex>>

Init ==
    /\ phase = "writing"
    /\ log = <<>>
    /\ tail = "none"
    /\ visible = OldRows
    /\ acknowledged = FALSE
    /\ replayIndex = 1

\* writeWALRecord makes separate writes for length, checksum, and payload.
\* These actions cover completed write calls, not crashes inside a call.
WriteClearLength ==
    /\ phase = "writing" /\ Len(log) = 0 /\ tail = "none"
    /\ tail' = "clear length"
    /\ UNCHANGED <<phase, log, visible, acknowledged, replayIndex>>

WriteClearChecksum ==
    /\ phase = "writing" /\ tail = "clear length"
    /\ tail' = "clear checksum"
    /\ UNCHANGED <<phase, log, visible, acknowledged, replayIndex>>

WriteClearPayload ==
    /\ phase = "writing" /\ tail = "clear checksum"
    /\ log' = Append(log, "clear")
    /\ tail' = "none"
    /\ UNCHANGED <<phase, visible, acknowledged, replayIndex>>

WriteInsertLength ==
    /\ phase = "writing" /\ Len(log) = 1 /\ tail = "none"
    /\ tail' = "insert length"
    /\ UNCHANGED <<phase, log, visible, acknowledged, replayIndex>>

WriteInsertChecksum ==
    /\ phase = "writing" /\ tail = "insert length"
    /\ tail' = "insert checksum"
    /\ UNCHANGED <<phase, log, visible, acknowledged, replayIndex>>

WriteInsertPayload ==
    /\ phase = "writing" /\ tail = "insert checksum"
    /\ log' = Append(log, "insert b")
    /\ tail' = "none"
    /\ UNCHANGED <<phase, visible, acknowledged, replayIndex>>

SyncWAL ==
    /\ phase = "writing" /\ Len(log) = 2 /\ tail = "none"
    /\ phase' = "synced"
    /\ UNCHANGED <<log, tail, visible, acknowledged, replayIndex>>

PublishRows ==
    /\ phase = "synced"
    /\ phase' = "published"
    /\ visible' = NewRows
    /\ UNCHANGED <<log, tail, acknowledged, replayIndex>>

ReturnSuccess ==
    /\ phase = "published"
    /\ phase' = "committed"
    /\ acknowledged' = TRUE
    /\ UNCHANGED <<log, tail, visible, replayIndex>>

\* At these boundaries the written prefix may survive a process crash even
\* before file.Sync; volatile rows are reloaded from the previous durable state.
Crash ==
    /\ phase \in {"writing", "synced", "published", "committed"}
    /\ phase' = "replaying"
    /\ visible' = OldRows
    /\ replayIndex' = 1
    /\ UNCHANGED <<log, tail, acknowledged>>

ReplayClear ==
    /\ phase = "replaying" /\ replayIndex <= Len(log)
    /\ log[replayIndex] = "clear"
    /\ visible' = {}
    /\ replayIndex' = replayIndex + 1
    /\ UNCHANGED <<phase, log, tail, acknowledged>>

ReplayInsert ==
    /\ phase = "replaying" /\ replayIndex <= Len(log)
    /\ log[replayIndex] = "insert b"
    /\ visible' = visible \cup {"b"}
    /\ replayIndex' = replayIndex + 1
    /\ UNCHANGED <<phase, log, tail, acknowledged>>

FinishRecovery ==
    /\ phase = "replaying" /\ replayIndex > Len(log) /\ tail = "none"
    /\ phase' = "recovered"
    /\ UNCHANGED <<log, tail, visible, acknowledged, replayIndex>>

RejectTornRecord ==
    /\ phase = "replaying" /\ replayIndex > Len(log) /\ tail # "none"
    /\ phase' = "refused"
    /\ UNCHANGED <<log, tail, visible, acknowledged, replayIndex>>

Done == phase \in {"recovered", "refused"} /\ UNCHANGED vars

Next == WriteClearLength \/ WriteClearChecksum \/ WriteClearPayload
        \/ WriteInsertLength \/ WriteInsertChecksum \/ WriteInsertPayload
        \/ SyncWAL \/ PublishRows \/ ReturnSuccess \/ Crash
        \/ ReplayClear \/ ReplayInsert \/ FinishRecovery \/ RejectTornRecord \/ Done

\* Open must expose the whole old or new image; success requires the new one.
AtomicRecovery ==
    phase = "recovered" =>
        /\ visible \in {OldRows, NewRows}
        /\ (acknowledged => visible = NewRows)
=============================================================================
