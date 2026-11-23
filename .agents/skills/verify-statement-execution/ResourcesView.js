export const title = 'Statement resource reservations';
export const state = v => v.closed.length === 2 ? (v.total === 0 ? "Both statements released" : "Released statements still hold capacity") : `Statements hold ${v.held.length} units, manager holds ${v.total}`;
export const step = (a,f,t) => ({Reserve:"Reserve one unit",Close:"Close one statement",Done:"Complete"})[a];
