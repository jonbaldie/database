export const title = "ScalarSequence";
export const state = v => v.phase === "released" ? `Read ${v.available} rows, returned ${v.outcome}` : `${v.phase}, read ${v.read} rows`;
export const step = a => ({NextCandidate:"Process next candidate",NextLeft:"Probe next left row",DeleteConflict:"Delete conflicting row",InsertCandidate:"Insert submitted row",ApplyActions:"Apply private schema actions",FailBeforeCommit:"Statement fails before commit"}[a] || a.replace(/([a-z])([A-Z])/g,"$1 $2"));
