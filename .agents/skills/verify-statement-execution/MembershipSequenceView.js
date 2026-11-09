export const title = "MembershipSequence";
export const state = v => v.outcome === "pending" ? `Inspect member ${v.position}` : `Membership ${v.outcome}; inputs ${v.values[1]}, ${v.values[2]}`;
export const step = a => ({NextCandidate:"Process next candidate",NextLeft:"Probe next left row",DeleteConflict:"Delete conflicting row",InsertCandidate:"Insert submitted row",ApplyActions:"Apply private schema actions",FailBeforeCommit:"Statement fails before commit"}[a] || a.replace(/([a-z])([A-Z])/g,"$1 $2"));
