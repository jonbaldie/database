export const title = 'Intersect duplicate rows';
export const state = v => v.cursor === v.left ? (v.emitted === Math.min(v.left,v.right) ? "Intersection complete" : "Too many duplicate rows returned") : `Left rows read ${v.cursor}, duplicates remaining ${v.remaining}`;
export const step = (a,f,t) => a === "Consume" ? (t.emitted > f.emitted ? "Return one matched occurrence" : "Discard unmatched occurrence") : "Complete";
