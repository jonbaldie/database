export const title="Subquery scope and reuse";
export const state=v=>v.phase==="complete"?(v.dependent && v.calls===1?"Dependent query reused wrong scope":"Completed query evaluations"):`${v.phase}, ${v.dependent?"dependent":"shared"}`;
export const step=a=>({Compile:"Classify correlation and cache independent result",Evaluate:"Evaluate for outer row",NextScope:"Advance outer row",Done:"Complete"})[a];
