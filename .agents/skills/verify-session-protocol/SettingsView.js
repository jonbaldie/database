export const title = "Session limit selected";
export const state = v => { if (v.value > 2) return "Session ceiling exceeded"; return ["Session limit selected", "Session limit assignment checked"][v.phase]; };
export const step = (a, from, to) => { if (a === "Assign") return to.value > 2 ? "Accept assignment above ceiling" : (from.requested < 1 || from.requested > 2) ? "Reject invalid assignment" : "Accept assignment within ceiling"; return ({"Assign": "Assign a session byte limit"})[a] || "Complete"; };
