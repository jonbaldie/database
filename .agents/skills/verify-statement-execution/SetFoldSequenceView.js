export const title = "SetFoldSequence";
export const state = v => v.phase === "complete" ? ((v.result.length===2) ? "Expected values published" : "Wrong values published") : v.phase;
export const step = a => a.replace(/([a-z])([A-Z])/g,"$1 $2");
