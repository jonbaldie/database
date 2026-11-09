export const title = "AlterPublication";
export const state = v => v.phase === "published" ? (v.valid ? "Valid schema published" : "Invalid schema published") : v.phase;
export const step = a => ({NextCandidate:"Process next candidate",NextLeft:"Probe next left row",DeleteConflict:"Delete conflicting row",InsertCandidate:"Insert submitted row",ApplyActions:"Apply private schema actions",FailBeforeCommit:"Statement fails before commit"}[a] || a.replace(/([a-z])([A-Z])/g,"$1 $2"));
