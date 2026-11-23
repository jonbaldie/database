export const title = 'Move a one-row window forward';
export const state = v => v.activeCount > 1 ? "Window contains an old row" : v.phase === "ready" ? `Window ready at row ${v.position+1}` : "Old row removed";
export const step = (a,f,t) => ({Remove:"Remove prior frame row",Add:"Add next frame row",Done:"Complete"})[a];
