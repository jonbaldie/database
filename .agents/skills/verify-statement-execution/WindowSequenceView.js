export const title = "WindowSequence";
export const state = v => v.phase === "complete" ? (v.rank === 3 ? "Peer ranks and frames complete" : "Wrong rank after peers") : `${v.phase}, row ${v.position}`;
export const step = a => ({NextCandidate:"Process next candidate",NextLeft:"Probe next left row",DeleteConflict:"Delete conflicting row",InsertCandidate:"Insert submitted row",ApplyActions:"Apply private schema actions",FailBeforeCommit:"Statement fails before commit"}[a] || a.replace(/([a-z])([A-Z])/g,"$1 $2"));
