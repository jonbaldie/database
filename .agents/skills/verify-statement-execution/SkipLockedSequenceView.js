export const title="Offset under SKIP LOCKED";
export const state=v=>v.phase==="complete"?`Returned row ${v.output[1]}`: `${v.phase}, candidate ${v.position}`;
export const step=a=>({Skip:"Inspect availability before counting offset",Acquire:"Lock and return candidate",Done:"Complete"})[a];
