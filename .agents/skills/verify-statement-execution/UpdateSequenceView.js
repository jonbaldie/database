export const title = "UpdateSequence";
export const state = v => v.phase === "complete" ? ((v.visible[1]===1 && v.visible[2]===1) ? "Expected values published" : "Wrong values published") : v.phase;
export const step = a => a.replace(/([a-z])([A-Z])/g,"$1 $2");
