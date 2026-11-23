export const title = 'Keep unmatched rows in a left join';
export const state = v => v.phase === "done" ? (Object.keys(v.output).length === 2 ? "Both left rows returned" : "Unmatched left row lost") : `Left row ${v.left}, ${v.phase}`;
export const step = (a,f,t) => a === "Match" ? (t.matched ? "Return matching pair" : "No right row matches") : a === "Finish" ? (Object.keys(t.output).length > Object.keys(f.output).length ? "Return unmatched row with nulls" : "Advance to next left row") : "Complete";
