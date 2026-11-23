export const title = 'Deliver a bounded row slice';
export const state = v => v.cursor === 3 || Object.keys(v.output).length === v.count ? (Object.values(v.output).join(",") === [0,1,2].filter(x => x >= v.offset && x < v.offset+v.count).join(",") ? "Requested row slice complete" : "Wrong row slice returned") : `Rows scanned ${v.cursor}, rows emitted ${Object.keys(v.output).length}`;
export const step = (a,f,t) => a === "Scan" ? (Object.keys(t.output).length > Object.keys(f.output).length ? "Emit next requested row" : "Skip offset row") : "Complete";
