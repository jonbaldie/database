export const title = 'Find an inclusive lower index bound';
export const state = v => v.low === v.high ? (v.low === v.boundary ? "Lower boundary found" : "Lower boundary is wrong") : `Search positions ${v.low} through ${v.high}`;
export const step = (a,f,t) => a === "Search" ? (t.low > f.low ? "Search above middle row" : "Search at or below middle row") : "Complete";
