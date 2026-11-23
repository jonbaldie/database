export const title = "Allocate identifiers up to the column limit";
export const state = v => v.exhausted ? "Identifier limit reached" : `Highest recorded ${Math.max(0,...v.seen)}, next identifier ${v.nextValue}`;
export const step = (a,from,to) => ({Allocate:"Allocate next identifier",Explicit:"Record an explicit positive identifier",Done:"Complete"})[a];
