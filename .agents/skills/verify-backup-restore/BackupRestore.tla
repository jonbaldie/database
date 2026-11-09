------------------------- MODULE BackupRestore -------------------------
EXTENDS Naturals
CONSTANT BrokenValidation, Fault
VARIABLES phase, valid, installed, existed, validated
vars == <<phase, valid, installed, existed, validated>>
Init == /\ phase = "archive available" /\ valid \in BOOLEAN /\ existed \in BOOLEAN
        /\ installed = 0 /\ validated = FALSE
Extract == /\ phase = "archive available" /\ phase' = "staged" /\ UNCHANGED <<valid, installed, existed, validated>>
Validate == /\ phase = "staged" /\ phase' = IF valid \/ BrokenValidation THEN "validated" ELSE "rejected"
            /\ validated' = valid /\ UNCHANGED <<valid, installed, existed>>
InstallNewDirectory == /\ phase = "validated" /\ ~existed /\ phase' = (IF Fault="partial new" THEN "install failed" ELSE "restored") /\ installed' = (IF Fault="partial new" THEN 1 ELSE 2)
                       /\ UNCHANGED <<valid, existed, validated>>
InstallFirstEntry == /\ phase = "validated" /\ existed /\ phase' = "partly installed" /\ installed' = 1
                     /\ UNCHANGED <<valid, existed, validated>>
InstallSecondEntry == /\ phase = "partly installed" /\ phase' = "entries installed" /\ installed' = (IF Fault="missing entry" THEN 1 ELSE 2)
                      /\ UNCHANGED <<valid, existed, validated>>
RemoveStaging == /\ phase = "entries installed" /\ phase' = "restored" /\ UNCHANGED <<valid, installed, existed, validated>>
InstallFails == /\ phase \in {"validated", "partly installed", "entries installed"}
                /\ phase' = "install failed" /\ UNCHANGED <<valid, installed, existed, validated>>
Done == /\ phase \in {"restored", "rejected", "install failed"} /\ UNCHANGED vars
Next == Extract \/ Validate \/ InstallNewDirectory \/ InstallFirstEntry \/ InstallSecondEntry \/ RemoveStaging \/ InstallFails \/ Done
OnlyValidatedFilesInstalled == installed > 0 => validated
RestoredHasBothEntries == phase = "restored" => installed = 2
NewDirectoryInstallIsWhole == ~existed => installed \in {0,2}
=============================================================================
