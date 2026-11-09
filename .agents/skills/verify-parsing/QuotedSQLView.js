export const title = "Read a quoted SQL literal";
export const state = v => {
 if(v.phase === "done") return v.accepted ? "Closing quote found" : "Unterminated literal rejected";
 const size = Object.keys(v.input).length;
 if(v.cursor > size) return "Input ended inside literal";
 const current = v.input[v.cursor];
 return current === "slash" ? "Check backslash escape" : current === "q" ? "Check quote delimiter" : "Read literal body";
};
export const step = (action, from, to) => {
 if(action === "Ordinary") return to.input[to.cursor] === "slash" ? "Read backslash escape" : to.input[to.cursor] === "q" ? "Read quote delimiter" : "Continue literal body";
 if(action === "Escape") return to.cursor > Object.keys(to.input).length ? "Skip escaped quote at end of input" : "Skip escaped character and reach next quote";
 return ({Doubled:"Skip doubled quote",Close:"Accept closing delimiter",Reject:"Reject missing closing delimiter",Done:"Complete"})[action];
};
