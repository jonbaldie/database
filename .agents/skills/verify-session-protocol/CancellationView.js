export const title = "Cancellation watch open";
export const state = v => { if (v.closes > 1) return "Same watch closed twice"; return ["Cancellation watch open", "Cancellation delivered", "Repeated cancellation ignored"][v.phase]; };
export const step = (a, from, to) => { if (a === "CancelAgain") return to.closes > 1 ? "Close the cancelled watch again" : "Ignore repeated cancellation"; return ({"Cancel": "Request cancellation", "CancelAgain": "Request cancellation again"})[a] || "Complete"; };
