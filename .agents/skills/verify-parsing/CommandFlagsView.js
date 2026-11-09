export const title = "Accept command flags only with nonempty, distinct settings";
export const state = v => v.phase === "parse" ? (v.cursor === 1 ? "Read first setting" : v.cursor === 2 ? "Read next setting" : "All settings read") : v.phase === "accepted" ? "Command flags accepted" : "Invalid flags rejected";
export const step = (action, from, to) => action === "Reject" ? (from.input[from.cursor].value === "" ? "Reject empty value" : "Reject repeated setting") : ({Add:"Store setting value",Accept:"Return parsed flags",Done:"Complete"})[action];
