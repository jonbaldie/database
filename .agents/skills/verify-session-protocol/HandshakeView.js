export const title = "Client sends handshake capabilities";
export const state = v => { if (v.phase === 1) return v.accepted ? "Capabilities accepted" : "Capabilities rejected"; return ["Client sends handshake capabilities", "Capabilities checked"][v.phase]; };
export const step = (a, from, to) => { if (a === "ValidateFlags") return to.accepted ? "Accept response capabilities" : "Reject missing capabilities"; return ({"ValidateFlags": "Check required protocol capabilities"})[a] || "Complete"; };
