---- MODULE WarningHistory ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, warnings
vars == <<phase, warnings>>
Init == /\ phase="ordinary statement" /\ warnings=0
AddWarning == /\ (phase="ordinary statement") /\ (phase'="warning added" /\ warnings'=1)
ShowWarnings == /\ (phase="warning added") /\ (phase'="shown once" /\ warnings'=IF Fault="show" THEN 0 ELSE warnings)
ShowAgain == /\ (phase="shown once") /\ (phase'="shown twice") /\ UNCHANGED <<warnings>>
BeginOrdinary == /\ (phase="shown twice") /\ (phase'="next statement" /\ warnings'=IF Fault="clear" THEN warnings ELSE 0)
StatementFails == /\ (phase="next statement") /\ (phase'="failure retained" /\ warnings'=1)
ResetConnection == /\ phase="failure retained" /\ phase'="reset" /\ warnings'=(IF Fault="reset" THEN warnings ELSE 0)
Done == /\ phase \in {"reset"} /\ UNCHANGED vars
Next == AddWarning \/ ShowWarnings \/ ShowAgain \/ BeginOrdinary \/ StatementFails \/ ResetConnection \/ Done
ShowRetainsWarnings == phase \in {"shown once","shown twice"} => warnings=1
OrdinaryClearsWarnings == phase="next statement" => warnings=0
ResetClearsWarnings == phase="reset" => warnings=0
====
