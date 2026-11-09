export const title = "SpillLifecycle";
export const state = v => v.phase === "closed" ? (v.files === 0 ? "Delivered rows and removed runs" : "Run files remain after close") : v.phase;
export const step = a => ({NextCandidate:"Process next candidate",NextLeft:"Probe next left row",DeleteConflict:"Delete conflicting row",InsertCandidate:"Insert submitted row",ApplyActions:"Apply private schema actions",FailBeforeCommit:"Statement fails before commit"}[a] || a.replace(/([a-z])([A-Z])/g,"$1 $2"));
