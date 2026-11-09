export const title = "Load catalog rows from durable storage";
export const state = v => v.phase === "open" ? "Open catalog and row storage" : (v.visible === "durable" ? "Use durable row image" : "Use legacy catalog rows");
export const step = (a,from,to) => a === "Hydrate" ? (from.engineRows === "durable" ? "Durable rows are present" : "Durable rows are empty; seed legacy rows") : "Complete";
