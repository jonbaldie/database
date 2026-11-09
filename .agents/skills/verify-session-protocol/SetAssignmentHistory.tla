---- MODULE SetAssignmentHistory ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, value, failure, database
vars == <<phase, value, failure, database>>
Init == /\ phase="before USE" /\ value=0 /\ failure=FALSE /\ database="app"
UseDatabase == /\ phase="before USE" /\ phase'="before SET" /\ database'=(IF Fault="database" THEN database ELSE "other") /\ UNCHANGED <<value,failure>>
FirstAssignment == /\ (phase="before SET") /\ (phase'="first applied" /\ value'=123) /\ UNCHANGED <<failure,database>>
UnknownAssignment == /\ (phase="first applied") /\ (phase'="assignment failed" /\ failure'=TRUE) /\ UNCHANGED <<value,database>>
StopAtFailure == /\ (phase="assignment failed") /\ (phase'="complete" /\ value'=IF Fault="continue" THEN 456 ELSE value) /\ UNCHANGED <<failure,database>>
Done == /\ phase \in {"complete"} /\ UNCHANGED vars
Next == UseDatabase \/ FirstAssignment \/ UnknownAssignment \/ StopAtFailure \/ Done
OnlySuccessfulPrefixApplied == phase="complete" => failure /\ value=123
SelectedDatabasePersists == phase="complete" => database="other"
====
