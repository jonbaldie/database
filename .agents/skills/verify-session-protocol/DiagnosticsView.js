export const title = "Caller owns a warning";
export const state = v => { if (v.phase === 2 && v.stored !== "original") return "Caller change altered session diagnostic"; return ["Caller owns a warning", "Session stores the warning", "Caller changes its warning"][v.phase]; };
export const step = (a, from, to) => ({"Replace": "Replace session diagnostics", "CallerChanges": "Change the caller diagnostic"})[a] || "Complete";
