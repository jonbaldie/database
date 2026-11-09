export const title = "No active explanation";
export const state = v => { if (v.phase === 3 && v.entry !== "new") return "Old cleanup removed the next statement"; return ["No active explanation", "First statement registered", "Next statement registered", "Old statement cleanup completes", "Next statement cleanup completes"][v.phase]; };
export const step = (a, from, to) => ({"BeginOld": "Start the first statement", "BeginNew": "Start the next statement", "FinishOld": "Finish cleanup for the first statement", "FinishNew": "Finish the next statement"})[a] || "Complete";
