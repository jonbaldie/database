export const title = 'Merge sorted spill runs';
export const state = v => Object.keys(v.output).length === 3 ? (JSON.stringify(Object.values(v.output)) === "[1,2,3]" ? "Sorted rows ready" : "Rows returned out of order") : `Rows merged ${Object.keys(v.output).length}`;
export const step = (a,f,t) => ({TakeLeft:"Return next row from first run",TakeRight:"Return next row from second run",Done:"Complete"})[a];
