---- MODULE SubqueryScopes ----
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES phase, dependent, outer, cached, calls, results
vars == <<phase,dependent,outer,cached,calls,results>>
Init == /\ phase="compile" /\ dependent \in BOOLEAN /\ outer=1 /\ cached=0 /\ calls=0 /\ results= <<>>
Compile == /\ phase="compile" /\ phase'="evaluate" /\ cached'=(IF ~dependent \/ Broken THEN 1 ELSE 0) /\ calls'=(IF ~dependent \/ Broken THEN 1 ELSE 0) /\ UNCHANGED <<dependent,outer,results>>
Evaluate == /\ phase="evaluate" /\ results'=Append(results,IF cached#0 THEN cached ELSE outer) /\ calls'=(IF cached#0 THEN calls ELSE calls+1) /\ phase'=(IF outer=2 THEN "complete" ELSE "next scope") /\ UNCHANGED <<dependent,outer,cached>>
NextScope == /\ phase="next scope" /\ outer'=2 /\ phase'="evaluate" /\ UNCHANGED <<dependent,cached,calls,results>>
Done == /\ phase="complete" /\ UNCHANGED vars
Next == Compile \/ Evaluate \/ NextScope \/ Done
ScopeControlsReuse == phase="complete" => /\ calls=(IF dependent THEN 2 ELSE 1) /\ results=(IF dependent THEN <<1,2>> ELSE <<1,1>>)
====
