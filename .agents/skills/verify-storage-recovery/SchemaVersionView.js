export const title = "Reject a transaction staged before a schema change";
export const state = v => v.phase === "staged" ? (v.schema === v.base ? "Transaction staged on current schema" : "Transaction holds prior schema") : v.phase === "finished" ? (v.published ? "Rows committed" : "Stale transaction rejected") : "Storage engine open";
export const step = a => ({Begin:"Stage row change",ChangeSchema:"Change table schema",Commit:"Try commit",Done:"Complete"})[a];
