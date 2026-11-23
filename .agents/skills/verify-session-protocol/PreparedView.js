export const title = "Prepared statement has no long data";
export const state = v => { if (v.phase === 2 && v.bytes > 0) return "Reset retained long data"; return ["Prepared statement has no long data", "Long data buffered", "Long data reset"][v.phase]; };
export const step = (a, from, to) => { if (a === "Reset") return to.bytes > 0 ? "Reset retains parameter bytes" : "Reset releases parameter bytes"; return ({"AppendLongData": "Append a parameter chunk", "Reset": "Reset prepared statement"})[a] || "Complete"; };
