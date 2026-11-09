export const title = 'Read two aliases of one CTE';
export const state = v => v.builds > 1 ? "CTE built twice" : v.references === 0 ? "CTE not read" : v.references === 1 ? "First alias materialized" : "Second alias reuses rows";
export const step = (a,f,t) => ({Materialize:"Build CTE rows",Reuse:"Reuse cached CTE rows",Done:"Complete"})[a];
