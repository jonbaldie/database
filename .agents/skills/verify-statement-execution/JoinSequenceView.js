export const title = "JoinSequence";
export const state = v => v.phase === "complete" ? (Object.keys(v.output).length === 2 ? "Matched and unmatched rows returned" : "Unmatched left row lost") : `${v.phase}, left row ${v.left}`;
export const step = a => ({NextCandidate:"Process next candidate",NextLeft:"Probe next left row",DeleteConflict:"Delete conflicting row",InsertCandidate:"Insert submitted row",ApplyActions:"Apply private schema actions",FailBeforeCommit:"Statement fails before commit"}[a] || a.replace(/([a-z])([A-Z])/g,"$1 $2"));
